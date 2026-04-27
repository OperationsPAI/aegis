"""Log-evidence dependency edge enricher — per-stack subclasses scan
application logs for backing-service failure patterns and emit inferred
``service|backing -[includes]→ span|caller_alarm`` edges.

Whereas ``inferred_edges.py`` uses *temporal co-anomaly* of
``container/pod`` ``unavailable`` and span anomalousness as the gate, this
enricher consults application logs for *direct evidence* that a service is
talking to a backing data service during the abnormal window. The classic
case is JDBC: when ``mysql`` is killed, OTel does not capture the JDBC
calls, so the trace graph has no edge from any caller to ``service|mysql``;
``inferred_edges.py``'s heuristic still applies (mysql ``unavailable`` +
caller spans anomalous), but log evidence makes the link explicit and
unambiguous.

Patterns are stack-specific:

- Java/Spring: ``HikariPool-1 - Failed to validate``,
  ``com.mysql.cj.jdbc.ConnectionImpl ... marked as broken``,
  ``SQLException``, ``CommunicationsException``.
- Go: ``dial tcp ... connect: connection refused``, ``no such host``,
  ``EOF`` on a SQL/Redis client.
- .NET: ``EFCore``, ``SqlException``.

Each subclass owns its own regex patterns; the shared kernel handles
abnormal-vs-normal diffing (subtract baseline noise), uniqueness gating
when the message does not name an explicit target, alarm-span lookup, and
edge emission.

Edges are emitted in the same shape as ``inferred_edges.py``:
``service|backing -[includes]→ span|caller::alarm`` so the existing
``RULE_SERVICE_TO_SPAN`` rule traverses them. Diagnostic metadata is
stored on ``HyperGraph.data["log_inferred_edges"]`` keyed by
``(src_id, dst_id)`` so post-hoc tooling can introspect why each edge was
added.
"""

from __future__ import annotations

import logging
import re
from abc import ABC, abstractmethod
from collections import defaultdict
from collections.abc import Iterable
from dataclasses import dataclass
from typing import ClassVar

import polars as pl

from rcabench_platform.v3.internal.reasoning.ir.adapters.inferred_edges import (
    _has_direct_call_dependency,
    _service_of_container,
    _service_of_pod,
)
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import LOADGEN_LIKE_SERVICES
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, Edge, HyperGraph, Node, PlaceKind

logger = logging.getLogger(__name__)


_LOG_LEVELS_TO_SCAN: tuple[str, ...] = ("ERROR", "WARN", "SEVERE")

# An infra-faulty service is a candidate target for log-derived dependency
# edges; we use the same strict signal as ``inferred_edges.py``.
_INFRA_FAULTY_STATES: frozenset[str] = frozenset({"unavailable"})

# Span anomaly states — alarm spans the propagator wants to reach.
_ANOMALOUS_SPAN_STATES: frozenset[str] = frozenset({"erroring", "unavailable", "missing", "slow"})


@dataclass(frozen=True, slots=True)
class LogEvidenceMetadata:
    """Diagnostic payload for each log-inferred edge.

    ``via`` records the adapter ``name`` so multiple stacks can coexist in
    a polyglot benchmark (otel-demo) and tooling can attribute each edge.
    """

    inferred: bool
    via: str
    abnormal_match_count: int
    normal_match_count: int
    explicit_target: bool


class LogDependencyAdapter(ABC):
    """Stack-specific log-evidence enricher base class.

    Subclasses declare:

    - ``name`` (class attr): registry key + ``via`` field on metadata.
    - ``applies(abnormal_logs)``: True iff this adapter recognises the
      logging conventions in the datapack (e.g. service names with the
      stack's prefix). Returning False makes ``dispatch_log_adapters``
      silently skip this adapter.
    - ``db_pool_error_pattern()``: polars-compatible regex string;
      message ``str.contains`` match → caller is failing to talk to its
      backing data service.
    - ``explicit_service_reference_pattern()`` (optional): regex with one
      capture group naming the target service (e.g. ``mysql`` extracted
      from ``com.mysql.cj.jdbc.ConnectionImpl``). When matched, kernel
      uses the captured name as the edge source; when not, kernel falls
      back to the unique infra suspect from timelines.

    The shared kernel handles diffing, uniqueness, and edge emission.
    """

    name: ClassVar[str]

    @abstractmethod
    def applies(self, abnormal_logs: pl.DataFrame) -> bool: ...

    @abstractmethod
    def db_pool_error_pattern(self) -> str: ...

    def explicit_service_reference_pattern(self) -> str | None:
        return None

    def enrich(
        self,
        graph: HyperGraph,
        timelines: dict[str, StateTimeline],
        abnormal_logs: pl.DataFrame,
        normal_logs: pl.DataFrame,
    ) -> int:
        """Mutate ``graph`` by adding inferred ``includes`` edges from
        log-implicated backing services to the caller's anomalous spans.

        Returns the number of edges added. ``0`` is a normal outcome (no
        DB-pool errors in the abnormal window, or every candidate already
        has a direct call-graph dependency).
        """
        if abnormal_logs.height == 0:
            return 0

        callers = self._diff_callers(abnormal_logs, normal_logs)
        if not callers:
            return 0

        # Resolve target service per caller: prefer the explicit name from
        # the log message; fall back to the unique infra suspect from
        # timelines. When neither is available, skip this caller —
        # picking one of multiple suspects without evidence would just
        # invent a dependency.
        caller_to_explicit = self._extract_explicit_targets(abnormal_logs, callers.keys())
        infra_suspects = _find_infra_suspect_services(graph, timelines)
        unique_suspect = next(iter(infra_suspects)) if len(infra_suspects) == 1 else None

        # Pre-index spans by owning service for fast caller-anomalous-spans
        # lookup. Span node names follow ``service::endpoint``.
        anom_spans_by_service = _anomalous_spans_grouped_by_service(graph, timelines)

        edges_added = 0
        for caller, (abnormal_count, normal_count) in callers.items():
            explicit = caller_to_explicit.get(caller)
            target_name = explicit or unique_suspect
            if target_name is None:
                logger.debug(
                    "log-dep[%s]: caller %s has DB-pool errors but no explicit target and %d infra suspects — skipping",
                    self.name,
                    caller,
                    len(infra_suspects),
                )
                continue
            target_node = graph.get_node_by_name(f"service|{target_name}")
            if target_node is None and explicit:
                # Backing services with no OTel instrumentation (e.g. JDBC
                # talks to ``mysql``) have no spans, so the graph builder
                # never creates a ``service|<name>`` node for them. When
                # the log message names the backend explicitly (regex
                # capture), look for matching pods and synthesise the
                # missing service + ``routes_to`` chain so the propagator
                # can traverse it like any other service.
                target_node = _synthesize_service_for_unspanned_backend(graph, target_name)
                if target_node is not None:
                    logger.info(
                        "log-dep[%s]: synthesised service|%s node + routes_to (%d pods)",
                        self.name,
                        target_name,
                        sum(
                            1
                            for _, dst, k in graph._graph.out_edges(target_node.id, keys=True)
                            if k == DepKind.routes_to
                        ),
                    )
            if target_node is None or target_node.id is None:
                logger.debug(
                    "log-dep[%s]: target service|%s not resolvable — skipping",
                    self.name,
                    target_name,
                )
                continue
            for span_node in anom_spans_by_service.get(caller, ()):
                assert span_node.id is not None
                if _has_direct_call_dependency(graph, target_node, span_node.id):
                    continue
                if _add_log_inferred_edge(
                    graph,
                    src_node=target_node,
                    dst_node=span_node,
                    via=self.name,
                    abnormal_count=abnormal_count,
                    normal_count=normal_count,
                    explicit_target=explicit is not None,
                ):
                    edges_added += 1
                    logger.info(
                        "log-inferred edge added: %s -> %s (via %s, abn=%d nrm=%d explicit=%s)",
                        target_node.uniq_name,
                        span_node.uniq_name,
                        self.name,
                        abnormal_count,
                        normal_count,
                        explicit is not None,
                    )

        return edges_added

    def _diff_callers(
        self,
        abnormal_logs: pl.DataFrame,
        normal_logs: pl.DataFrame,
    ) -> dict[str, tuple[int, int]]:
        """Return ``{service_name: (abnormal_count, normal_count)}`` for every
        service whose abnormal-window error count exceeds its normal-window
        count. Filtering at the row level (level + pattern) is done here.
        """
        if "service_name" not in abnormal_logs.columns or "message" not in abnormal_logs.columns:
            return {}
        if "level" not in abnormal_logs.columns:
            return {}

        pat = self.db_pool_error_pattern()
        ab = abnormal_logs.filter(
            pl.col("level").is_in(list(_LOG_LEVELS_TO_SCAN)) & pl.col("message").str.contains(pat)
        )
        if ab.height == 0:
            return {}

        if normal_logs.height > 0 and {"service_name", "message", "level"}.issubset(set(normal_logs.columns)):
            nl = normal_logs.filter(
                pl.col("level").is_in(list(_LOG_LEVELS_TO_SCAN)) & pl.col("message").str.contains(pat)
            )
        else:
            nl = abnormal_logs.head(0)

        ab_count = ab.group_by("service_name").len().rename({"len": "a"})
        nl_count = nl.group_by("service_name").len().rename({"len": "n"}) if nl.height > 0 else None

        if nl_count is not None:
            diff = ab_count.join(nl_count, on="service_name", how="left").fill_null(0)
        else:
            diff = ab_count.with_columns(pl.lit(0, dtype=pl.UInt32).alias("n"))

        diff = diff.filter(pl.col("a") > pl.col("n"))
        return {row["service_name"]: (int(row["a"]), int(row["n"])) for row in diff.iter_rows(named=True)}

    def _extract_explicit_targets(
        self,
        abnormal_logs: pl.DataFrame,
        callers: Iterable[str],
    ) -> dict[str, str]:
        """For each caller, return the first explicit target name captured by
        ``explicit_service_reference_pattern()``. Callers absent from the
        result use the unique-suspect fallback.
        """
        pat = self.explicit_service_reference_pattern()
        if pat is None:
            return {}
        compiled = re.compile(pat)
        result: dict[str, str] = {}
        callers_set = set(callers)
        # Iterate over abnormal-only error rows again — the diff method
        # already filtered to relevant levels but threw away the rows.
        rows = abnormal_logs.filter(
            pl.col("service_name").is_in(list(callers_set)) & pl.col("level").is_in(list(_LOG_LEVELS_TO_SCAN))
        ).select(["service_name", "message"])
        for row in rows.iter_rows(named=True):
            svc = row["service_name"]
            if svc in result:
                continue
            m = compiled.search(row["message"])
            if m and m.groups():
                result[svc] = m.group(1)
        return result


# ---------------------------------------------------------------------------
# Module-level registry + dispatcher
# ---------------------------------------------------------------------------

_REGISTRY: dict[str, type[LogDependencyAdapter]] = {}


def register_log_adapter(cls: type[LogDependencyAdapter]) -> type[LogDependencyAdapter]:
    """Decorator: add subclass to the module-level log-adapter registry.

    Duplicate registration raises (re-registering masks bugs where two
    adapters claim the same ``name``).
    """
    name = getattr(cls, "name", None)
    if not name:
        raise ValueError(f"{cls.__name__} must define class attribute 'name'")
    if name in _REGISTRY:
        raise ValueError(f"log adapter '{name}' already registered by {_REGISTRY[name].__name__}")
    _REGISTRY[name] = cls
    return cls


def get_registered_log_adapters() -> dict[str, type[LogDependencyAdapter]]:
    return dict(_REGISTRY)


def _clear_log_registry_for_tests() -> None:
    _REGISTRY.clear()


def dispatch_log_adapters(
    graph: HyperGraph,
    timelines: dict[str, StateTimeline],
    abnormal_logs: pl.DataFrame,
    normal_logs: pl.DataFrame,
) -> int:
    """Run every registered adapter whose ``applies`` says yes against the
    given datapack. Returns total inferred edges added across all adapters.

    Adapters whose ``applies`` returns False are silently skipped — a
    polyglot benchmark (otel-demo) registers multiple subclasses, each
    fanning out only on rows that match its stack's service-naming
    convention.
    """
    if abnormal_logs.height == 0:
        return 0
    total = 0
    for adapter_cls in _REGISTRY.values():
        adapter = adapter_cls()
        if not adapter.applies(abnormal_logs):
            continue
        try:
            n = adapter.enrich(graph, timelines, abnormal_logs, normal_logs)
        except Exception:
            logger.exception("log adapter %s failed; continuing with remaining adapters", adapter.name)
            continue
        total += n
    return total


# ---------------------------------------------------------------------------
# Shared helpers
# ---------------------------------------------------------------------------


def _find_infra_suspect_services(
    graph: HyperGraph,
    timelines: dict[str, StateTimeline],
) -> set[str]:
    """Return service names whose container or pod is ever in
    ``_INFRA_FAULTY_STATES`` during the timeline window. Loadgens are
    excluded — they are synthetic traffic, never causal.
    """
    suspects: set[str] = set()
    for node_key, tl in timelines.items():
        if tl.kind not in (PlaceKind.container, PlaceKind.pod):
            continue
        if not tl.ever_in_any(_INFRA_FAULTY_STATES):
            continue
        node = graph.get_node_by_name(node_key)
        if node is None or node.id is None:
            continue
        service_node = (
            _service_of_container(graph, node.id) if tl.kind == PlaceKind.container else _service_of_pod(graph, node.id)
        )
        if service_node is None:
            continue
        if service_node.self_name in LOADGEN_LIKE_SERVICES:
            continue
        suspects.add(service_node.self_name)
    return suspects


def _anomalous_spans_grouped_by_service(
    graph: HyperGraph,
    timelines: dict[str, StateTimeline],
) -> dict[str, list[Node]]:
    """Return ``{service_name: [span_node, ...]}`` for every span that hit
    an anomalous state during the abnormal window.

    Spans use the ``self_name == "service::endpoint"`` convention; we
    extract the service via ``split("::", 1)[0]``.
    """
    by_service: dict[str, list[Node]] = defaultdict(list)
    for node_key, tl in timelines.items():
        if tl.kind != PlaceKind.span:
            continue
        if not tl.ever_in_any(_ANOMALOUS_SPAN_STATES):
            continue
        node = graph.get_node_by_name(node_key)
        if node is None or node.id is None:
            continue
        if "::" not in node.self_name:
            continue
        owning_service = node.self_name.split("::", 1)[0]
        by_service[owning_service].append(node)
    return by_service


def _synthesize_service_for_unspanned_backend(graph: HyperGraph, target_name: str) -> Node | None:
    """Create a missing ``service|<target_name>`` node + ``routes_to`` edges
    to every matching pod, returning the new service node.

    Only invoked when an explicit log-evidence target (e.g. ``mysql`` from
    ``com.mysql.cj.jdbc.``) names a backing service that has pods/containers
    in the graph but no service node — typical of OTel-uninstrumented
    backends like SQL/Redis/Kafka, whose pods are visible to k8s metrics
    but generate no spans, so the graph builder (which derives services
    from spans) never instantiates a service node.

    Returns ``None`` if no matching pod exists — in that case the target
    name is ambiguous (typo? loadgen? rabbitmq vs rabbitmq-leader?) and
    we refuse to make something up.

    Pod-name match: ``pod_self_name == target_name`` (rare) OR
    ``pod_self_name.startswith(target_name + "-")`` covering both
    StatefulSet pods (``mysql-0``) and Deployment pods
    (``mysql-647dffc4-abc12``).
    """
    matching_pods: list[Node] = []
    for nid in graph._graph.nodes():
        node = graph.get_node_by_id(nid)
        if node is None or node.kind != PlaceKind.pod:
            continue
        if node.self_name == target_name or node.self_name.startswith(f"{target_name}-"):
            matching_pods.append(node)
    if not matching_pods:
        return None
    svc_node = graph.add_node(Node(kind=PlaceKind.service, self_name=target_name))
    assert svc_node.id is not None
    for pod in matching_pods:
        assert pod.id is not None
        graph.add_edge(
            Edge(
                src_id=svc_node.id,
                dst_id=pod.id,
                src_name=svc_node.uniq_name,
                dst_name=pod.uniq_name,
                kind=DepKind.routes_to,
                weight=1.0,
                data=None,
            ),
            strict=False,
        )
    return svc_node


def _add_log_inferred_edge(
    graph: HyperGraph,
    *,
    src_node: Node,
    dst_node: Node,
    via: str,
    abnormal_count: int,
    normal_count: int,
    explicit_target: bool,
) -> bool:
    """Insert ``service|S -> span|A::*`` as an ``includes`` edge.

    Mirrors ``inferred_edges._add_inferred_edge`` but writes metadata to a
    distinct key (``"log_inferred_edges"``) so introspection can
    distinguish the two enrichers' edges.
    """
    assert src_node.id is not None and dst_node.id is not None
    if graph._graph.has_edge(src_node.id, dst_node.id, DepKind.includes):
        return False
    edge = Edge(
        src_id=src_node.id,
        dst_id=dst_node.id,
        src_name=src_node.uniq_name,
        dst_name=dst_node.uniq_name,
        kind=DepKind.includes,
        weight=1.0,
        data=None,
    )
    graph.add_edge(edge, strict=False)
    metadata_store = graph.data.setdefault("log_inferred_edges", {})
    metadata_store[(src_node.id, dst_node.id)] = LogEvidenceMetadata(
        inferred=True,
        via=via,
        abnormal_match_count=abnormal_count,
        normal_match_count=normal_count,
        explicit_target=explicit_target,
    )
    return True


# ---------------------------------------------------------------------------
# TrainTicket (Java/Spring Boot) subclass
# ---------------------------------------------------------------------------


@register_log_adapter
class TrainTicketLogAdapter(LogDependencyAdapter):
    """Java/Spring Boot — Hikari connection-pool + JDBC class-name patterns.

    Verified on ``ts0-mysql-container-kill-9t6n24``: abnormal window has
    11 ``HikariPool-1 - Failed to validate connection
    com.mysql.cj.jdbc.ConnectionImpl@... (No operations allowed after
    connection closed.)`` rows on each of ``ts-train-service`` and
    ``ts-auth-service``; normal window has 0 matches. The
    ``com\\.(mysql|...)\\.`` capture extracts the target DB name directly,
    so we do not need uniqueness-gating fallback for this stack in
    practice — but it is wired up regardless for stacks that log generic
    ``SQLException`` without naming the driver.
    """

    name: ClassVar[str] = "trainticket"

    # Hikari is the canonical Spring Boot connection pool; pattern
    # captures the two classes of message we see in real failures
    # (``Failed to validate``, ``marked as broken``) plus the broader
    # ``SQLException`` / ``Hibernate ... exception`` / ``Communications
    # Exception`` net for stacks that wrap JDBC differently. Servlet
    # request-processing failures rooted in transaction/JDBC exceptions
    # are also captured via the ``request processing failed`` template.
    _DB_POOL_PATTERN: ClassVar[str] = (
        r"(?i)HikariPool|"
        r"hibernate.*exception|"
        r"SQLException|"
        r"CommunicationsException|"
        r"request processing failed.*(transaction|jdbc|sql)"
    )

    # JDBC driver class names embed the target DB. Capturing group 1 is
    # ``mysql`` / ``postgresql`` / etc. — a service name we can look up
    # directly. Hibernate exceptions sometimes carry the bare driver
    # class string too.
    _EXPLICIT_DB_PATTERN: ClassVar[str] = r"com\.(mysql|postgresql|mariadb|oracle|microsoft\.sqlserver)\."

    def applies(self, abnormal_logs: pl.DataFrame) -> bool:
        if "service_name" not in abnormal_logs.columns:
            return False
        ts_rows = abnormal_logs.filter(pl.col("service_name").str.starts_with("ts-"))
        return ts_rows.height > 0

    def db_pool_error_pattern(self) -> str:
        return self._DB_POOL_PATTERN

    def explicit_service_reference_pattern(self) -> str | None:
        return self._EXPLICIT_DB_PATTERN


__all__: tuple[str, ...] = (
    "LogDependencyAdapter",
    "LogEvidenceMetadata",
    "TrainTicketLogAdapter",
    "dispatch_log_adapters",
    "get_registered_log_adapters",
    "register_log_adapter",
)
