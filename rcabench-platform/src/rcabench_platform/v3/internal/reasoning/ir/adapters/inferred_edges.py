"""Inferred call-graph edge enricher — temporal co-anomaly + infra suspicion.

Closes the trace-blind dependency gap: when a service S is killed and another
service's spans go anomalous, but the call path S↔alarm_span is invisible in
OTel traces (e.g. Spring's auth filter intercepts requests before any
controller span starts, so no child span is emitted for the auth call), the
propagator currently has no edge to traverse from S to alarm_span and reports
``no_paths`` even though the right faulty service was identified by k8s
metrics.

Unlike ``StateAdapter`` implementations, this module does not produce
``Transition`` events. It mutates the ``HyperGraph`` topology in place by
adding *inferred* ``includes`` edges from a faulty ``service|S`` node to an
anomalous ``span|X::Y`` node owned by a different service, when:

    1. S is *infra-faulty*: at least one of S's container or pod nodes was
       observed in ``unavailable`` state during the abnormal window
       (``unavailable`` is the strict "node actually went down" signal;
       ``degraded`` alone is excluded because CPU pressure cascades to many
       neighbours and creates too many candidate causes).
    2. The number of distinct services with ``unavailable`` infra is at most
       ``_MAX_FAULTY_SERVICES_PER_CASE`` (uniqueness gate — when too many
       services simultaneously go down, structural inheritance + namespace
       cascading already accounts for the propagation, and inferred edges
       would only add noise).
    3. ``span|X::Y`` is *anomalous* (state in {erroring, unavailable, missing,
       slow}) at least once during a window where S is infra-faulty
       (temporal-coincidence gate: at least one bucket of co-anomaly).
    4. There is no *direct call-graph parent* in the existing graph — i.e.
       no span owned by S has a ``calls`` edge to or from ``span|X::Y``. If
       a direct call dependency already exists, the trace topology already
       covers the case and the propagator's standard rules can traverse it.
    5. S is not the span's own service (already covered by structural
       inheritance) and is not in ``LOADGEN_LIKE_SERVICES`` (synthetic
       traffic, not a meaningful causal source).

Edges are emitted with ``kind=DepKind.includes`` so the existing
``RULE_SERVICE_TO_SPAN`` rule (confidence 0.85) traverses them — using
``calls`` would not match any existing rule for ``src_kind=service`` and the
edge would never be traversed. The edge's pydantic ``data`` field is left
``None``; introspection metadata (``inferred=True``, co-anomaly window
counts, uniqueness flag) is stored on ``HyperGraph.data["inferred_edges"]``
keyed by ``(src_id, dst_id)`` so the ``Edge`` model stays unchanged.
"""

from __future__ import annotations

import logging
from collections import defaultdict
from collections.abc import Iterable
from dataclasses import dataclass

from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import LOADGEN_LIKE_SERVICES
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, Edge, HyperGraph, Node, PlaceKind

logger = logging.getLogger(__name__)

# A service is "infra-faulty" if any of its container/pod nodes hits this
# state at any point. ``degraded`` is intentionally NOT here — that captures
# CPU pressure and propagates to too many cascade victims, generating noisy
# candidate causes. ``unavailable`` is the strict "the node actually went
# down" signal.
_INFRA_FAULTY_STATES: frozenset[str] = frozenset({"unavailable"})

# Span states that we consider "anomalous" — these are the states that ought
# to have an upstream causal explanation in the graph.
_ANOMALOUS_SPAN_STATES: frozenset[str] = frozenset({"erroring", "unavailable", "missing", "slow"})

# Bucket width in seconds. The IR timelines have variable-length windows so
# we discretise on a uniform 5-second grid for co-anomaly counting.
_BUCKET_SECONDS: int = 5

# Maximum distinct infra-faulty services in a case before we suppress all
# inferred edges — too many simultaneous failures and the heuristic gives
# wild guesses. This is a per-case gate (the spec calls for a per-window
# gate, but in practice the IR pipeline produces case-wide windows of
# state, so per-case and per-window collapse to the same thing for the
# strict ``unavailable`` signal).
_MAX_FAULTY_SERVICES_PER_CASE: int = 2


@dataclass(frozen=True, slots=True)
class InferredEdgeMetadata:
    """Diagnostic payload attached to each inferred edge.

    Stored in ``HyperGraph.data["inferred_edges"][(src_id, dst_id)]``; the
    propagator does not read it, but post-hoc tooling (PR validation,
    confidence-weighting follow-ups) introspects it to explain why an
    edge was added.
    """

    inferred: bool
    co_anomaly_windows: int
    total_anomaly_windows: int
    unique_infra_suspect: bool


def _service_name_of_span(span_self_name: str) -> str | None:
    """Span ``self_name`` is ``"<service>::<span_name>"``; return the service
    half. Returns ``None`` if the span name does not match the convention
    (e.g. synthetic test fixtures may use plain names).
    """
    if "::" not in span_self_name:
        return None
    return span_self_name.split("::", 1)[0]


def _service_of_pod(graph: HyperGraph, pod_id: int) -> Node | None:
    """Walk ``routes_to`` backwards from a pod to its service. Returns the
    first service found (a pod is normally routed to by exactly one service).
    """
    for src_id, _dst_id, edge_key in graph._graph.in_edges(pod_id, keys=True):  # type: ignore[call-arg]
        if edge_key == DepKind.routes_to:
            return graph.get_node_by_id(src_id)
    return None


def _service_of_container(graph: HyperGraph, container_id: int) -> Node | None:
    """Walk ``runs`` then ``routes_to`` backwards from a container to its
    service. Returns the first match or ``None``.
    """
    for src_id, _dst_id, edge_key in graph._graph.in_edges(container_id, keys=True):  # type: ignore[call-arg]
        if edge_key == DepKind.runs:
            svc = _service_of_pod(graph, src_id)
            if svc is not None:
                return svc
    return None


def _bucket_indices_for_state(
    timeline: StateTimeline,
    states: frozenset[str],
    grid_start: int,
    grid_end: int,
) -> set[int]:
    """Return the set of bucket indices in ``[grid_start, grid_end)`` where
    ``timeline``'s state is in ``states``.

    Buckets are ``_BUCKET_SECONDS`` wide; bucket ``i`` covers
    ``[grid_start + i*BUCKET, grid_start + (i+1)*BUCKET)``.
    """
    indices: set[int] = set()
    if grid_end <= grid_start:
        return indices
    n_buckets = (grid_end - grid_start + _BUCKET_SECONDS - 1) // _BUCKET_SECONDS
    for window in timeline.windows:
        if window.state not in states:
            continue
        # Compute overlapping bucket range.
        w_start = max(window.start, grid_start)
        w_end = min(window.end, grid_end)
        if w_end <= w_start:
            continue
        first_bucket = (w_start - grid_start) // _BUCKET_SECONDS
        # ``end`` is exclusive — a window that ends exactly at a bucket
        # boundary should not include that bucket.
        last_bucket_exclusive = (w_end - grid_start + _BUCKET_SECONDS - 1) // _BUCKET_SECONDS
        for b in range(first_bucket, min(last_bucket_exclusive, n_buckets)):
            indices.add(b)
    return indices


def _grid_bounds(timelines: dict[str, StateTimeline]) -> tuple[int, int]:
    """Return ``(grid_start, grid_end)`` covering every timeline window. The
    returned range is in unix seconds; callers use ``_BUCKET_SECONDS`` to
    discretise.
    """
    starts: list[int] = []
    ends: list[int] = []
    for tl in timelines.values():
        for w in tl.windows:
            starts.append(w.start)
            ends.append(w.end)
    if not starts:
        return 0, 0
    return min(starts), max(ends)


def _faulty_buckets_per_service(
    graph: HyperGraph,
    timelines: dict[str, StateTimeline],
    grid_start: int,
    grid_end: int,
) -> dict[str, set[int]]:
    """Map ``service_name -> {bucket_indices where service is infra-faulty}``.

    A service is infra-faulty in a bucket if any of its container or pod
    timelines is in ``_INFRA_FAULTY_STATES`` (i.e. ``unavailable``) during
    that bucket. Loadgen-like services are excluded.
    """
    by_service: dict[str, set[int]] = defaultdict(set)
    for node_key, tl in timelines.items():
        if tl.kind not in (PlaceKind.container, PlaceKind.pod):
            continue
        node = graph.get_node_by_name(node_key)
        if node is None or node.id is None:
            continue
        service_node = (
            _service_of_container(graph, node.id) if tl.kind == PlaceKind.container else _service_of_pod(graph, node.id)
        )
        if service_node is None:
            continue
        service_name = service_node.self_name
        if service_name in LOADGEN_LIKE_SERVICES:
            continue
        buckets = _bucket_indices_for_state(tl, _INFRA_FAULTY_STATES, grid_start, grid_end)
        if buckets:
            by_service[service_name].update(buckets)
    return by_service


def _anomaly_buckets_per_span(
    timelines: dict[str, StateTimeline],
    grid_start: int,
    grid_end: int,
) -> dict[str, set[int]]:
    """Map ``span_node_key -> {bucket_indices where span is anomalous}``.

    Only spans whose state is in ``_ANOMALOUS_SPAN_STATES`` during at least
    one bucket are returned.
    """
    by_span: dict[str, set[int]] = {}
    for node_key, tl in timelines.items():
        if tl.kind != PlaceKind.span:
            continue
        buckets = _bucket_indices_for_state(tl, _ANOMALOUS_SPAN_STATES, grid_start, grid_end)
        if buckets:
            by_span[node_key] = buckets
    return by_span


def _has_direct_call_dependency(graph: HyperGraph, service_node: Node, alarm_span_id: int) -> bool:
    """Return ``True`` if any span owned by ``service_node`` has a ``calls``
    edge directly to or from ``alarm_span_id``.

    This is the "direct call-graph dependency" check. A transitive walk over
    the entire call graph is too lenient: every backend service is reachable
    from a frontend hub via deep call chains, so a global ``has_path`` check
    would suppress every inferred edge. We restrict to a single ``calls`` hop
    (after the service-to-its-spans hop, which always exists), which catches
    the common "the trace already directly observes the dependency" case
    without false-suppressing the trace-blind cases this adapter targets.
    """
    assert service_node.id is not None
    g = graph._graph
    own_span_ids: list[int] = []
    for _src, dst, key in g.out_edges(service_node.id, keys=True):  # type: ignore[call-arg]
        if key == DepKind.includes:
            own_span_ids.append(dst)
    for span_id in own_span_ids:
        if g.has_edge(span_id, alarm_span_id, DepKind.calls):
            return True
        if g.has_edge(alarm_span_id, span_id, DepKind.calls):
            return True
    return False


def _add_inferred_edge(
    graph: HyperGraph,
    *,
    src_node: Node,
    dst_node: Node,
    co_anomaly_windows: int,
    total_anomaly_windows: int,
    unique_infra_suspect: bool,
) -> bool:
    """Insert ``service|S -> span|X::Y`` as an ``includes`` edge. Returns
    ``True`` if a new edge was added, ``False`` if one already exists.

    Diagnostic metadata is stored on ``graph.data["inferred_edges"]`` keyed
    by ``(src_id, dst_id)`` so the ``Edge`` pydantic model stays unchanged
    (its ``data`` field is typed as ``CallsEdgeData | None``, which doesn't
    fit our payload).
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
    metadata_store = graph.data.setdefault("inferred_edges", {})
    metadata_store[(src_node.id, dst_node.id)] = InferredEdgeMetadata(
        inferred=True,
        co_anomaly_windows=co_anomaly_windows,
        total_anomaly_windows=total_anomaly_windows,
        unique_infra_suspect=unique_infra_suspect,
    )
    return True


def enrich_with_inferred_edges(
    graph: HyperGraph,
    timelines: dict[str, StateTimeline],
) -> int:
    """Mutate ``graph`` by adding inferred ``includes`` edges from infra-faulty
    services to anomalous spans they have no direct call-graph dependency to.

    Returns the number of edges added. See module docstring for the full
    heuristic.
    """
    grid_start, grid_end = _grid_bounds(timelines)
    if grid_end <= grid_start:
        return 0

    faulty_per_service = _faulty_buckets_per_service(graph, timelines, grid_start, grid_end)
    if not faulty_per_service:
        return 0

    # Case-wide uniqueness gate: too many simultaneously-unavailable services
    # produces noisy guesses; bail out entirely.
    if len(faulty_per_service) > _MAX_FAULTY_SERVICES_PER_CASE:
        logger.info(
            "inferred-edge gate: %d unavailable services exceeds limit %d, skipping",
            len(faulty_per_service),
            _MAX_FAULTY_SERVICES_PER_CASE,
        )
        return 0

    anomaly_per_span = _anomaly_buckets_per_span(timelines, grid_start, grid_end)
    if not anomaly_per_span:
        return 0

    is_unique_suspect = len(faulty_per_service) == 1

    # Pre-resolve service nodes; skip services that don't exist as nodes.
    service_nodes: dict[str, Node] = {}
    for service_name in faulty_per_service:
        node = graph.get_node_by_name(f"service|{service_name}")
        if node is not None:
            service_nodes[service_name] = node

    edges_added = 0
    for span_key, span_anomaly_buckets in anomaly_per_span.items():
        span_node = graph.get_node_by_name(span_key)
        if span_node is None or span_node.id is None:
            continue
        # ``span|<service>::<endpoint>`` — extract owning service to skip
        # same-service pairs (already covered by structural inheritance).
        own_service = _service_name_of_span(span_node.self_name)
        total = len(span_anomaly_buckets)
        if total == 0:
            continue

        for service_name, faulty_buckets in faulty_per_service.items():
            if service_name == own_service:
                continue
            src_node = service_nodes.get(service_name)
            if src_node is None or src_node.id is None:
                continue
            # Temporal coincidence: at least one bucket where the service is
            # infra-faulty AND the alarm span is anomalous. With strict
            # ``unavailable`` as the source signal, even a single bucket of
            # overlap is meaningful — short container kills produce brief
            # ``unavailable`` windows but the alarm span stays anomalous
            # well past the kill (the cascade outlasts the trigger).
            co = span_anomaly_buckets & faulty_buckets
            if not co:
                continue
            # Direct call-graph dependency check: skip if S already has a
            # span with a direct ``calls`` edge to/from the alarm span.
            if _has_direct_call_dependency(graph, src_node, span_node.id):
                continue
            if _add_inferred_edge(
                graph,
                src_node=src_node,
                dst_node=span_node,
                co_anomaly_windows=len(co),
                total_anomaly_windows=total,
                unique_infra_suspect=is_unique_suspect,
            ):
                edges_added += 1
                logger.info(
                    "inferred edge added: %s -> %s (co-anomaly %d/%d windows, unique=%s)",
                    src_node.uniq_name,
                    span_node.uniq_name,
                    len(co),
                    total,
                    is_unique_suspect,
                )

    return edges_added


__all__: Iterable[str] = ("enrich_with_inferred_edges", "InferredEdgeMetadata")
