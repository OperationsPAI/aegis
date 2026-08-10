"""Trace -> DB binding adapter — synthesise the missing call edge from Java
services to a database service that emits no OTel server spans.

Closes a trace-blind gap on TrainTicket and any benchmark that uses a
client-only OTel instrumentation for its database tier. The Spring/Hibernate
JDBC instrumentation reports the SQL statement as a *client* span name (e.g.
``SELECT ts.users``) on the **caller** side, but the database itself never
runs an OTel SDK so no server-side span exists. The trace edge builder in
``parquet_loader._build_edges_from_traces`` therefore never emits a
``span -> span calls`` edge into a ``mysql`` node, the ``mysql`` service node
itself is never created (``_build_service_nodes_from_traces`` only enumerates
service names that appear in trace data), and the propagator from a
``container|mysql`` injection has no reachable subgraph.

This adapter detects DB client spans by **span_name regex** (because this
dataset has no standardised ``attr.db.*`` attributes — they are simply
absent), groups them by caller service, and:

1. Materialises the ``service|mysql`` node when at least one mysql infra node
   exists (``container|mysql`` / ``pod|mysql-*`` / ``stateful_set|mysql``).
2. Wires the structural containment edges
   (``stateful_set|mysql --manages--> pod|mysql-*`` and
   ``service|mysql --routes_to--> pod|mysql-*``) so
   ``StructuralInheritanceAdapter`` cascades the observed ``container|mysql``
   ``unavailable`` state up to ``service|mysql`` and from there to the JDBC
   span endpoints.
3. Adds ``service|<caller> --calls--> service|mysql`` as evidence of the
   logical dependency (one edge per caller; carries the SQL-span count and
   error signal for downstream diagnostics).
4. Adds ``service|mysql --includes--> span|<caller>::<sql>`` so the existing
   ``RULE_SERVICE_TO_SPAN`` (``src_kind=service``, ``edge_kind=includes``)
   actually traverses from the mysql service node into the JDBC span
   endpoints — without this hop the propagator cannot reach the alarm root
   spans through the synthesised topology.

The adapter is per-system. Subclasses declare ``applies()`` so that benchmarks
without the SQL-naming convention (otel-demo, sockshop, hotel-reservation,
social-network, media-microservices, teastore) cannot accidentally activate
it. ``dispatch_trace_db_binding_adapters`` is the single entry-point and is
invoked from ``cli.py`` strictly **before** ``enrich_with_inferred_edges`` and
the propagator construction so that the synthesised topology participates in
both the inferred-edge heuristic and the BFS traversal.
"""

from __future__ import annotations

import abc
import logging
import re
from collections.abc import Iterable
from dataclasses import dataclass
from typing import ClassVar

import polars as pl

from rcabench_platform.v3.internal.reasoning.models.graph import (
    CallsEdgeData,
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)

logger = logging.getLogger(__name__)


# Registry pattern mirrors the StateAdapter family — keeps the dispatch entry
# point a one-liner and lets future per-system adapters (e.g. otel-demo's
# postgres binding, if its instrumentation regresses) plug in without
# touching cli.py.
_REGISTRY: list[type[TraceDbBindingAdapter]] = []


def register_trace_db_binding_adapter(cls: type[TraceDbBindingAdapter]) -> type[TraceDbBindingAdapter]:
    _REGISTRY.append(cls)
    return cls


@dataclass(frozen=True, slots=True)
class TraceDbBinding:
    """A single (caller_service -> db_target_service) binding.

    ``span_count`` is the number of detected DB client spans on the abnormal
    side; ``error_count`` is how many of those carried a 5xx HTTP response
    status (a coarse error signal — DB client spans don't have a dedicated
    status field in this schema, but Spring's RestController wrapper folds
    SQL-driven 500s back into the HTTP response code on the same trace).
    """

    caller_service: str
    db_target_service: str
    span_count: int
    error_count: int
    sample_span_names: tuple[str, ...]


@dataclass(frozen=True, slots=True)
class TraceDbBindingMetadata:
    """Diagnostic payload attached to each synthesised edge.

    Stored on ``HyperGraph.data["trace_db_bindings"]`` keyed by
    ``(src_id, dst_id, kind)``. Mirrors the convention used by
    ``inferred_edges.InferredEdgeMetadata``.
    """

    adapter_name: str
    caller_service: str
    db_target_service: str
    span_count: int
    error_count: int


class TraceDbBindingAdapter(abc.ABC):
    """Per-system DB client-span detector.

    Subclasses pick a system signature in ``applies()`` and a span-detection
    rule in ``detect_db_client_spans()``; the dispatcher takes care of the
    graph mutation. The split keeps system-specific knowledge (regex,
    service-name prefix, target DB) co-located in the subclass.
    """

    name: ClassVar[str]
    db_target_service: ClassVar[str]

    @abc.abstractmethod
    def applies(self, abnormal_traces: pl.DataFrame) -> bool:
        """Return True iff the system signature this adapter binds is present."""

    @abc.abstractmethod
    def detect_db_client_spans(self, abnormal_traces: pl.DataFrame) -> pl.DataFrame:
        """Return rows of detected DB client spans.

        The returned frame must contain at least the columns
        ``service_name`` and ``span_name``; if the source frame has
        ``attr.http.response.status_code`` the implementation is expected to
        preserve it so error counts can be aggregated.
        """

    def collect_bindings(self, abnormal_traces: pl.DataFrame) -> list[TraceDbBinding]:
        spans = self.detect_db_client_spans(abnormal_traces)
        if spans.height == 0:
            return []

        # Aggregate per caller service. Keep up to a handful of sample span
        # names per caller for diagnostics.
        has_status = "attr.http.response.status_code" in spans.columns
        agg_exprs: list[pl.Expr] = [
            pl.len().alias("span_count"),
            pl.col("span_name").unique().head(5).alias("sample_span_names"),
        ]
        if has_status:
            status = pl.col("attr.http.response.status_code")
            agg_exprs.append((status.is_not_null() & (status >= 500)).cast(pl.Int64).sum().alias("error_count"))
        else:
            agg_exprs.append(pl.lit(0, dtype=pl.Int64).alias("error_count"))

        grouped = spans.group_by("service_name").agg(agg_exprs).sort("service_name")
        bindings: list[TraceDbBinding] = []
        for row in grouped.iter_rows(named=True):
            bindings.append(
                TraceDbBinding(
                    caller_service=row["service_name"],
                    db_target_service=self.db_target_service,
                    span_count=int(row["span_count"]),
                    error_count=int(row["error_count"]),
                    sample_span_names=tuple(row["sample_span_names"] or ()),
                )
            )
        return bindings


# SQL statement opener — empirically detects 252 abnormal-window client spans
# across 13 services on ts0-mysql-container-kill-9t6n24 with zero false
# positives (every other client span is HTTP-prefixed). Schema half of the
# span name (``SELECT ts.<table>``) is uniformly ``ts`` so all bindings target
# the single ``service|mysql`` node.
_TT_SQL_OPENER_RE: re.Pattern[str] = re.compile(r"^(?i:SELECT|INSERT|UPDATE|DELETE|REPLACE)\s+")


@register_trace_db_binding_adapter
class TrainTicketTraceDbBindingAdapter(TraceDbBindingAdapter):
    """TrainTicket Spring/Hibernate JDBC -> mysql binding.

    Activates iff at least one ``ts-*`` service appears in the abnormal trace
    frame. The DB target is the singleton ``mysql`` service.
    """

    name: ClassVar[str] = "trainticket_trace_db_binding"
    db_target_service: ClassVar[str] = "mysql"

    def applies(self, abnormal_traces: pl.DataFrame) -> bool:
        if abnormal_traces.height == 0 or "service_name" not in abnormal_traces.columns:
            return False
        return abnormal_traces.filter(pl.col("service_name").str.starts_with("ts-")).height > 0

    def detect_db_client_spans(self, abnormal_traces: pl.DataFrame) -> pl.DataFrame:
        if abnormal_traces.height == 0:
            return abnormal_traces
        if "span_name" not in abnormal_traces.columns or "service_name" not in abnormal_traces.columns:
            return abnormal_traces.head(0)
        # Restrict to ts-* callers: avoids picking up SQL-shaped span names
        # from foreign workloads that may share the same parquet partition.
        return abnormal_traces.filter(
            pl.col("service_name").str.starts_with("ts-") & pl.col("span_name").str.contains(_TT_SQL_OPENER_RE.pattern)
        )


def _mysql_infra_present(graph: HyperGraph, db_target: str) -> bool:
    """Return True iff at least one infra node ties to the DB target.

    The adapter must NOT manufacture a service node out of thin air — it only
    materialises one when k8s metrics already evidence the database exists
    (container/pod/stateful_set with the target name). On a fixture that has
    no such infra, dispatch is a no-op (test #5).
    """
    return (
        graph.get_node_by_name(f"{PlaceKind.container}|{db_target}") is not None
        or graph.get_node_by_name(f"{PlaceKind.stateful_set}|{db_target}") is not None
        or any(n.kind == PlaceKind.pod and n.self_name.startswith(f"{db_target}-") for n in graph._node_id_map.values())
        or graph.get_node_by_name(f"{PlaceKind.pod}|{db_target}") is not None
    )


def _mysql_pod_nodes(graph: HyperGraph, db_target: str) -> list[Node]:
    direct = graph.get_node_by_name(f"{PlaceKind.pod}|{db_target}")
    if direct is not None:
        return [direct]
    return [
        n for n in graph._node_id_map.values() if n.kind == PlaceKind.pod and n.self_name.startswith(f"{db_target}-")
    ]


def _ensure_service_node(graph: HyperGraph, service_name: str) -> Node:
    existing = graph.get_node_by_name(f"{PlaceKind.service}|{service_name}")
    if existing is not None:
        return existing
    return graph.add_node(Node(kind=PlaceKind.service, self_name=service_name))


def _record_metadata(
    graph: HyperGraph,
    *,
    src_id: int,
    dst_id: int,
    kind: DepKind,
    adapter_name: str,
    binding: TraceDbBinding,
) -> None:
    store = graph.data.setdefault("trace_db_bindings", {})
    key = (src_id, dst_id, kind)
    if key in store:
        return
    store[key] = TraceDbBindingMetadata(
        adapter_name=adapter_name,
        caller_service=binding.caller_service,
        db_target_service=binding.db_target_service,
        span_count=binding.span_count,
        error_count=binding.error_count,
    )


def _add_edge_idempotent(
    graph: HyperGraph,
    *,
    src: Node,
    dst: Node,
    kind: DepKind,
    data: CallsEdgeData | None = None,
) -> bool:
    assert src.id is not None and dst.id is not None
    if graph._graph.has_edge(src.id, dst.id, kind):
        return False
    graph.add_edge(
        Edge(
            src_id=src.id,
            dst_id=dst.id,
            src_name=src.uniq_name,
            dst_name=dst.uniq_name,
            kind=kind,
            weight=1.0,
            data=data,
        ),
        strict=False,
    )
    return True


def _wire_db_structural_edges(graph: HyperGraph, db_service: Node, db_target: str) -> None:
    """Connect db service / stateful_set to its pods so structural inheritance
    can cascade ``container.unavailable`` -> ``service.unavailable``.

    Only the edges actually needed by ``StructuralInheritanceAdapter`` are
    added: ``service --routes_to--> pod`` (so pod->service propagation has a
    reverse path) and ``stateful_set --manages--> pod`` for completeness.
    """
    pods = _mysql_pod_nodes(graph, db_target)
    sset = graph.get_node_by_name(f"{PlaceKind.stateful_set}|{db_target}")
    for pod in pods:
        _add_edge_idempotent(graph, src=db_service, dst=pod, kind=DepKind.routes_to)
        if sset is not None:
            _add_edge_idempotent(graph, src=sset, dst=pod, kind=DepKind.manages)


def _bind_caller_to_db(
    graph: HyperGraph,
    *,
    adapter_name: str,
    db_service: Node,
    binding: TraceDbBinding,
) -> int:
    """Materialise the (caller_service, db_target) binding in the graph.

    Returns the count of newly added edges (for logging / idempotency check).
    """
    edges_added = 0
    caller = graph.get_node_by_name(f"{PlaceKind.service}|{binding.caller_service}")
    if caller is None:
        # The caller service has SQL spans but no service node — that means
        # ``_build_service_nodes_from_traces`` skipped it (e.g. fixture has no
        # trace rows for that service), so we bail rather than fabricate. The
        # service node will reappear if real trace data is loaded.
        return 0

    # service|caller --calls--> service|mysql. Carries the abnormal-window
    # span and error counts for downstream diagnostics. CallsEdgeData
    # baseline counts stay zero here — this synthetic edge has no baseline
    # observation; it is purely "we saw this dependency in the abnormal data".
    calls_data = CallsEdgeData(
        abnormal_call_count=binding.span_count,
        abnormal_error_count=binding.error_count,
    )
    if _add_edge_idempotent(graph, src=caller, dst=db_service, kind=DepKind.calls, data=calls_data):
        assert caller.id is not None and db_service.id is not None
        _record_metadata(
            graph,
            src_id=caller.id,
            dst_id=db_service.id,
            kind=DepKind.calls,
            adapter_name=adapter_name,
            binding=binding,
        )
        edges_added += 1

    # service|mysql --includes--> span|<caller>::<sql_name> for each detected
    # SQL span. This is the hop that actually makes the propagator reachable
    # because RULE_SERVICE_TO_SPAN is the only rule with src_kind=service in
    # the canonical rule set.
    for span_name in binding.sample_span_names:
        span_uniq = f"{PlaceKind.span}|{binding.caller_service}::{span_name}"
        span_node = graph.get_node_by_name(span_uniq)
        if span_node is None:
            continue
        if _add_edge_idempotent(graph, src=db_service, dst=span_node, kind=DepKind.includes):
            assert db_service.id is not None and span_node.id is not None
            _record_metadata(
                graph,
                src_id=db_service.id,
                dst_id=span_node.id,
                kind=DepKind.includes,
                adapter_name=adapter_name,
                binding=binding,
            )
            edges_added += 1
    return edges_added


def dispatch_trace_db_binding_adapters(
    graph: HyperGraph,
    abnormal_traces: pl.DataFrame,
    normal_traces: pl.DataFrame | None = None,
) -> int:
    """Run every registered adapter that ``applies()`` to ``abnormal_traces``.

    Returns the total number of edges added across all adapters. Idempotent:
    re-running on the same graph and frames produces no additional edges.
    """
    del normal_traces  # currently unused; reserved for adapters that want to
    # diff abnormal vs baseline DB calls (e.g. detect "DB call disappeared
    # entirely in abnormal" as a stronger signal).

    if abnormal_traces is None or abnormal_traces.height == 0:
        return 0

    total_added = 0
    for adapter_cls in _REGISTRY:
        adapter = adapter_cls()
        if not adapter.applies(abnormal_traces):
            continue
        if not _mysql_infra_present(graph, adapter.db_target_service):
            logger.debug(
                "trace-db-binding %s skipped: no infra node for db_target=%s",
                adapter.name,
                adapter.db_target_service,
            )
            continue
        bindings = adapter.collect_bindings(abnormal_traces)
        if not bindings:
            continue
        db_service = _ensure_service_node(graph, adapter.db_target_service)
        _wire_db_structural_edges(graph, db_service, adapter.db_target_service)
        for binding in bindings:
            total_added += _bind_caller_to_db(
                graph,
                adapter_name=adapter.name,
                db_service=db_service,
                binding=binding,
            )
        logger.info(
            "trace-db-binding %s: %d bindings -> %d edges (db_target=%s)",
            adapter.name,
            len(bindings),
            total_added,
            adapter.db_target_service,
        )
    return total_added


__all__ = [
    "TraceDbBinding",
    "TraceDbBindingAdapter",
    "TraceDbBindingMetadata",
    "TrainTicketTraceDbBindingAdapter",
    "dispatch_trace_db_binding_adapters",
    "register_trace_db_binding_adapter",
]
