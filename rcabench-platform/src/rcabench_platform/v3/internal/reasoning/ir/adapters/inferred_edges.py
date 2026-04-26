"""Inferred call-graph edges anchored at the injection point.

Implements the §7.3 "injection-anchored inferred edges" formulation from
``docs/reasoning-feature-taxonomy.md``. The earlier "co-anomaly bridge"
formulation (any silent service × any alarm span = candidate edge) is
explicitly REJECTED there: it generated O(silent × alarm) edges and let
correlated-but-not-causal anomalies form spurious paths. This module
replaces it.

An inferred edge is a *patch on missing observation*. The source of every
inferred edge is a node tied to the **physical injection point**
(``physical_node_ids`` from the upstream pipeline). Two narrow scenarios
are emitted:

**Scenario A — Class A: dead pod has no spans**
    Trigger: an injection node of kind ``pod`` or ``container`` is in
    state ``unavailable`` at any point in its timeline (the abnormal
    window the IR pipeline has summarised).
    Source: the dead pod / container node.
    Targets: services that physically depend on the dead pod via k8s
    ownership — ``service --routes_to-> pod`` predecessors, or for a
    container ``service --routes_to-> pod --runs-> container``.
    Metadata kind: ``"depends_on_dead_infra"``.

**Scenario E — Class E: gating-failure silences downstream operations**
    Trigger: an injection node of kind ``service`` is in state
    ``erroring`` or ``silent`` AND its name matches a gating-service
    substring hint (``auth``, ``gateway``, ``verify``, ``verification``,
    ``dns``, ``login``).
    Source: the gating service node.
    Targets: services whose timeline shows ``silent`` or ``missing``
    during abnormal AND that are reachable forward from the gating
    service via ``calls`` / ``includes`` edges within ``_MAX_FORWARD_HOPS``,
    AND whose silence is not already explained by a strictly-closer
    erroring ancestor in the same forward BFS.
    Metadata kind: ``"gated_silenced"``.

Both scenarios use ``DepKind.includes`` for the actual edge inserted into
the graph (so the existing ``RULE_SERVICE_TO_SPAN``-style propagator rules
can still traverse them); the textual scenario tag
(``depends_on_dead_infra`` / ``gated_silenced``) is stored in
``HyperGraph.data["inferred_edges"][(src,dst)] = InferredEdgeMetadata(...)``
for introspection. No new ``DepKind`` values are added.
"""

from __future__ import annotations

import logging
from collections import deque
from collections.abc import Iterable
from dataclasses import dataclass

from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, Edge, HyperGraph, Node, PlaceKind

logger = logging.getLogger(__name__)

# Substring hints (case-insensitive) that classify a service as "gating".
# A service whose name contains any of these tokens is treated as a
# precondition for downstream traffic to fire (auth, gateway, verification,
# DNS, login). Per-system override is intentionally out of scope — this
# stays a documented heuristic.
_GATING_SERVICE_HINTS: tuple[str, ...] = (
    "auth",
    "gateway",
    "verify",
    "verification",
    "dns",
    "login",
)

# States that mark an injection container/pod as "dead infra" (Scenario A).
_DEAD_INFRA_STATES: frozenset[str] = frozenset({"unavailable"})

# States that mark a gating service as having failed (Scenario E source).
_GATING_FAILURE_STATES: frozenset[str] = frozenset({"erroring", "silent"})

# States that mark a downstream service as "silenced" (Scenario E target).
_SILENCED_TARGET_STATES: frozenset[str] = frozenset({"silent", "missing"})

# An ERRORING service ancestor closer than the gating source explains the
# downstream silence; in that case Scenario E does not emit an edge.
_ERRORING_STATES: frozenset[str] = frozenset({"erroring"})

# Forward BFS depth from the gating service when collecting Scenario E
# candidates. Five hops covers typical request chains in the benchmarks
# without globally connecting unrelated services.
_MAX_FORWARD_HOPS: int = 5

# Edges traversed by Scenario E's forward BFS from the gating service. We
# follow service-to-span (``includes``) and span-to-span (``calls``) so the
# walk transits service → span → ... → span → service via the trace graph.
_FORWARD_EDGE_KINDS: frozenset[DepKind] = frozenset({DepKind.calls, DepKind.includes})


@dataclass(frozen=True, slots=True)
class InferredEdgeMetadata:
    """Diagnostic payload attached to each inferred edge.

    Stored in ``HyperGraph.data["inferred_edges"][(src_id, dst_id)]``; the
    propagator does not read it, but post-hoc tooling introspects it to
    explain which §7.3 scenario emitted the edge.
    """

    inferred: bool
    kind: str  # "depends_on_dead_infra" | "gated_silenced"


def _is_gating_service(service_self_name: str) -> bool:
    """Return ``True`` if the service name contains any gating substring."""
    lower = service_self_name.lower()
    return any(hint in lower for hint in _GATING_SERVICE_HINTS)


def _service_of_pod(graph: HyperGraph, pod_id: int) -> Node | None:
    """Walk ``routes_to`` backwards from a pod to its service. Returns the
    first service found (a pod is normally routed to by exactly one service).

    Retained for re-use by ``log_dependency.py`` — the helper is general
    k8s-ownership walking, not tied to any specific scenario.
    """
    for src_id, _dst_id, edge_key in graph._graph.in_edges(pod_id, keys=True):  # type: ignore[call-arg]
        if edge_key == DepKind.routes_to:
            return graph.get_node_by_id(src_id)
    return None


def _service_of_container(graph: HyperGraph, container_id: int) -> Node | None:
    """Walk ``runs`` then ``routes_to`` backwards from a container to its
    service. Returns the first match or ``None``.

    Retained for re-use by ``log_dependency.py``.
    """
    for src_id, _dst_id, edge_key in graph._graph.in_edges(container_id, keys=True):  # type: ignore[call-arg]
        if edge_key == DepKind.runs:
            svc = _service_of_pod(graph, src_id)
            if svc is not None:
                return svc
    return None


def _has_direct_call_dependency(graph: HyperGraph, service_node: Node, alarm_span_id: int) -> bool:
    """Return ``True`` if any span owned by ``service_node`` has a ``calls``
    edge directly to or from ``alarm_span_id``.

    Retained for re-use by ``log_dependency.py``: the log-evidence adapters
    still need this trace-blind dependency check before emitting their own
    inferred edges.
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


def _timeline_ever_in(timelines: dict[str, StateTimeline], node_uniq: str, states: frozenset[str]) -> bool:
    """Return ``True`` if ``timelines[node_uniq]`` ever enters any of ``states``.

    Returns ``False`` if the node has no timeline.
    """
    tl = timelines.get(node_uniq)
    if tl is None:
        return False
    return tl.ever_in_any(states)


def _add_inferred_edge(
    graph: HyperGraph,
    *,
    src_node: Node,
    dst_node: Node,
    kind: str,
) -> bool:
    """Insert ``src -> dst`` as a ``DepKind.includes`` edge with metadata.

    Returns ``True`` if a new edge was added, ``False`` if one already
    exists in the graph. Diagnostic metadata is stored on
    ``graph.data["inferred_edges"]`` keyed by ``(src_id, dst_id)`` because
    the ``Edge`` pydantic model's ``data`` field is typed as
    ``CallsEdgeData | None`` and would not accept our payload.
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
        kind=kind,
    )
    return True


def _scenario_a_consumer_services(graph: HyperGraph, dead_node: Node) -> list[Node]:
    """Find services that physically consume a dead pod or container.

    For a dead **pod**: walk ``in_edges`` filtered by ``routes_to`` — the
    predecessors are the consuming services.
    For a dead **container**: walk ``in_edges`` filtered by ``runs`` to
    find the owning pod, then recurse into the pod case.
    """
    assert dead_node.id is not None
    g = graph._graph
    consumers: list[Node] = []
    seen_service_ids: set[int] = set()

    if dead_node.kind == PlaceKind.pod:
        for src_id, _dst_id, edge_key in g.in_edges(dead_node.id, keys=True):  # type: ignore[call-arg]
            if edge_key != DepKind.routes_to:
                continue
            if src_id in seen_service_ids:
                continue
            candidate = graph.get_node_by_id(src_id)
            if candidate.kind != PlaceKind.service:
                continue
            seen_service_ids.add(src_id)
            consumers.append(candidate)
    elif dead_node.kind == PlaceKind.container:
        for pod_src_id, _dst_id, edge_key in g.in_edges(dead_node.id, keys=True):  # type: ignore[call-arg]
            if edge_key != DepKind.runs:
                continue
            pod_node = graph.get_node_by_id(pod_src_id)
            if pod_node.kind != PlaceKind.pod:
                continue
            for svc_id, _pod_id, svc_edge_key in g.in_edges(pod_src_id, keys=True):  # type: ignore[call-arg]
                if svc_edge_key != DepKind.routes_to:
                    continue
                if svc_id in seen_service_ids:
                    continue
                candidate = graph.get_node_by_id(svc_id)
                if candidate.kind != PlaceKind.service:
                    continue
                seen_service_ids.add(svc_id)
                consumers.append(candidate)
    return consumers


def _apply_scenario_a(
    graph: HyperGraph,
    timelines: dict[str, StateTimeline],
    injection_node: Node,
) -> int:
    """Emit Scenario A inferred edges for one injection node, if applicable.

    Returns the number of edges added.
    """
    if injection_node.kind not in (PlaceKind.pod, PlaceKind.container):
        return 0
    if not _timeline_ever_in(timelines, injection_node.uniq_name, _DEAD_INFRA_STATES):
        return 0

    consumers = _scenario_a_consumer_services(graph, injection_node)
    edges_added = 0
    for consumer in consumers:
        if _add_inferred_edge(
            graph,
            src_node=injection_node,
            dst_node=consumer,
            kind="depends_on_dead_infra",
        ):
            edges_added += 1
            logger.info(
                "inferred edge added (Scenario A): %s -> %s [depends_on_dead_infra]",
                injection_node.uniq_name,
                consumer.uniq_name,
            )
    return edges_added


def _forward_bfs_service_levels(
    graph: HyperGraph,
    source_id: int,
    max_hops: int,
) -> dict[int, int]:
    """Forward BFS from ``source_id`` along baseline request flow.

    Returns ``{service_node_id: hop_count_from_source}`` covering every
    service node reachable within ``max_hops`` (the source itself is
    included at hop 0 if it is a service). Non-service intermediate nodes
    (e.g. spans) are walked through but not present in the returned dict.

    Edges traversed (modelling caller→callee request flow as
    ``caller_service --includes-> caller_span --calls-> callee_span
    <-includes-- callee_service``):

    - ``includes`` and ``calls`` edges out of the current node, AND
    - ``includes`` edges *into* the current node when the current node is
      a span (climbing from a span to its owning service so the BFS can
      then re-descend into other spans/services).
    """
    g = graph._graph
    visited: dict[int, int] = {source_id: 0}
    queue: deque[tuple[int, int]] = deque([(source_id, 0)])
    service_levels: dict[int, int] = {}
    src_node = graph.get_node_by_id(source_id)
    if src_node.kind == PlaceKind.service:
        service_levels[source_id] = 0
    while queue:
        node_id, depth = queue.popleft()
        if depth >= max_hops:
            continue
        node = graph.get_node_by_id(node_id)
        # Forward step 1: outgoing includes / calls.
        for _src, dst_id, edge_key in g.out_edges(node_id, keys=True):  # type: ignore[call-arg]
            if edge_key not in _FORWARD_EDGE_KINDS:
                continue
            if dst_id in visited:
                continue
            visited[dst_id] = depth + 1
            dst_node = graph.get_node_by_id(dst_id)
            if dst_node.kind == PlaceKind.service:
                service_levels.setdefault(dst_id, depth + 1)
            queue.append((dst_id, depth + 1))
        # Forward step 2: when at a span, climb to its owning service via
        # the incoming ``includes`` edge so the BFS can continue into the
        # callee-service's other spans/services.
        if node.kind == PlaceKind.span:
            for owner_id, _dst, edge_key in g.in_edges(node_id, keys=True):  # type: ignore[call-arg]
                if edge_key != DepKind.includes:
                    continue
                if owner_id in visited:
                    continue
                visited[owner_id] = depth + 1
                owner_node = graph.get_node_by_id(owner_id)
                if owner_node.kind == PlaceKind.service:
                    service_levels.setdefault(owner_id, depth + 1)
                queue.append((owner_id, depth + 1))
    return service_levels


def _apply_scenario_e(
    graph: HyperGraph,
    timelines: dict[str, StateTimeline],
    injection_node: Node,
) -> int:
    """Emit Scenario E inferred edges for one gating-service injection.

    Returns the number of edges added.
    """
    if injection_node.kind != PlaceKind.service:
        return 0
    if not _is_gating_service(injection_node.self_name):
        return 0
    if not _timeline_ever_in(timelines, injection_node.uniq_name, _GATING_FAILURE_STATES):
        return 0
    assert injection_node.id is not None

    service_levels = _forward_bfs_service_levels(graph, injection_node.id, _MAX_FORWARD_HOPS)
    if not service_levels:
        return 0
    gating_level = service_levels.get(injection_node.id, 0)

    # Pre-compute which reachable services are ERRORING — those become
    # potential "closer ancestor" explanations for downstream silences.
    erroring_levels: dict[int, int] = {}
    for svc_id, level in service_levels.items():
        if svc_id == injection_node.id:
            continue
        svc_node = graph.get_node_by_id(svc_id)
        if _timeline_ever_in(timelines, svc_node.uniq_name, _ERRORING_STATES):
            erroring_levels[svc_id] = level

    edges_added = 0
    for svc_id, target_level in service_levels.items():
        if svc_id == injection_node.id:
            continue
        target_node = graph.get_node_by_id(svc_id)
        if not _timeline_ever_in(timelines, target_node.uniq_name, _SILENCED_TARGET_STATES):
            continue
        # Strictly-closer ERRORING ancestor explains the silence.
        if any(
            ancestor_level < target_level and ancestor_level > gating_level
            for ancestor_id, ancestor_level in erroring_levels.items()
            if ancestor_id != svc_id
        ):
            continue
        if _add_inferred_edge(
            graph,
            src_node=injection_node,
            dst_node=target_node,
            kind="gated_silenced",
        ):
            edges_added += 1
            logger.info(
                "inferred edge added (Scenario E): %s -> %s [gated_silenced]",
                injection_node.uniq_name,
                target_node.uniq_name,
            )
    return edges_added


def enrich_with_inferred_edges(
    graph: HyperGraph,
    timelines: dict[str, StateTimeline],
    injection_node_ids: list[int],
) -> int:
    """Mutate ``graph`` by adding §7.3 injection-anchored inferred edges.

    For each id in ``injection_node_ids``, look up the node in the graph
    and dispatch to Scenario A (dead pod/container -> consumer services)
    or Scenario E (gating service erroring/silent -> downstream silenced
    services). See module docstring for the full formulation.

    Returns the total number of edges added across all injection nodes
    and scenarios.
    """
    if not injection_node_ids:
        return 0

    edges_added = 0
    for inj_id in injection_node_ids:
        if inj_id not in graph._node_id_map:
            logger.warning("inferred-edges: injection node id %d not in graph, skipping", inj_id)
            continue
        injection_node = graph.get_node_by_id(inj_id)
        edges_added += _apply_scenario_a(graph, timelines, injection_node)
        edges_added += _apply_scenario_e(graph, timelines, injection_node)
    return edges_added


__all__: Iterable[str] = ("enrich_with_inferred_edges", "InferredEdgeMetadata")
