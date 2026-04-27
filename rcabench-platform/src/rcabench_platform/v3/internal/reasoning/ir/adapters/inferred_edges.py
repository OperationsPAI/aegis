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

# Anomalous span states that mark consumers worth bridging from a dead-infra
# injection (Scenario A). Excludes ``healthy`` (uninteresting) and
# ``unknown`` (no observation). Includes the canonical request-layer
# anomalous palette so downstream rules (``span_*_to_caller``,
# ``service_to_span``) admit the consumer's chain.
_SCENARIO_A_TARGET_SPAN_STATES: frozenset[str] = frozenset({"missing", "slow", "erroring", "unavailable", "silent"})

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
    """Find services that consume the dead pod/container via baseline trace
    call relationships.

    The methodology spec for Scenario A targets read literally as
    "services with routes_to to the dead pod" — that resolves only to the
    pod's *owner* service (a self-loop within the dead infra). The intent
    documented in the same paragraph is "services that depend on this
    pod" — i.e. consumers in the request-flow sense, derivable from
    baseline trace ``calls`` edges (per §7.4 invariant 2: baseline edges
    are preserved in the merged graph even when the abnormal window
    drops them).

    Walk: dead → owner service → ``includes`` → owner spans →
    ``in_edges`` calls → caller spans → ``in_edges`` includes → caller
    services. Distinct caller services minus the owner itself are the
    consumers.
    """
    assert dead_node.id is not None
    g = graph._graph

    # Step 1: resolve dead_node to its owner service.
    if dead_node.kind == PlaceKind.pod:
        owner = _service_of_pod(graph, dead_node.id)
    elif dead_node.kind == PlaceKind.container:
        owner = _service_of_container(graph, dead_node.id)
    else:
        return []
    if owner is None or owner.id is None:
        return []

    # Step 2: collect owner's spans (service --includes--> span).
    owner_span_ids: list[int] = []
    for _src, dst_id, edge_key in g.out_edges(owner.id, keys=True):  # type: ignore[call-arg]
        if edge_key == DepKind.includes:
            dst_node = graph.get_node_by_id(dst_id)
            if dst_node is not None and dst_node.kind == PlaceKind.span:
                owner_span_ids.append(dst_id)
    if not owner_span_ids:
        return []

    # Step 3: walk back from owner spans through ``calls`` to find caller
    # spans, then up via ``includes`` to find caller services.
    consumers: list[Node] = []
    seen: set[int] = {owner.id}
    for owner_span_id in owner_span_ids:
        for caller_span_id, _dst_id, edge_key in g.in_edges(owner_span_id, keys=True):  # type: ignore[call-arg]
            if edge_key != DepKind.calls:
                continue
            for caller_svc_id, _span_id, includes_key in g.in_edges(caller_span_id, keys=True):  # type: ignore[call-arg]
                if includes_key != DepKind.includes:
                    continue
                if caller_svc_id in seen:
                    continue
                candidate = graph.get_node_by_id(caller_svc_id)
                if candidate is None or candidate.kind != PlaceKind.service:
                    continue
                # Skip loadgen-side callers — they're synthetic traffic, not
                # consumer services of the dead infra.
                if candidate.self_name.lower() in {
                    "loadgenerator",
                    "load-generator",
                    "locust",
                    "wrk2",
                    "dsb-wrk2",
                    "k6",
                }:
                    continue
                seen.add(caller_svc_id)
                consumers.append(candidate)
    return consumers


def _apply_scenario_a(
    graph: HyperGraph,
    timelines: dict[str, StateTimeline],
    injection_node: Node,
) -> int:
    """Emit Scenario A inferred edges for one injection node, if applicable.

    Edge shape: ``service|owner --includes--> span|consumer_anomalous_span``.
    The owner service inherits ``unavailable`` from the dead container/pod
    (see ``StructuralInheritanceAdapter``), and the existing
    ``service_to_span`` rule (src_states includes ``unavailable``,
    dst_states includes ``missing/slow/erroring/unavailable``) admits this
    edge during BFS — so the propagator can reach consumer alarms even
    though no real ``calls`` edge from consumer to the dead infra exists
    (the dead service emits no abnormal-window spans).

    Targets are consumer services' spans whose timeline shows a non-nominal
    state during the abnormal window. Restricting to anomalous spans keeps
    the inferred fan-out bounded — a typical TT case has ~10 consumer
    spans with anomalous state per dead infra, vs. 100s of consumer spans
    in total.

    Returns the number of edges added.
    """
    if injection_node.kind not in (PlaceKind.pod, PlaceKind.container):
        return 0
    if injection_node.id is None:
        return 0
    if not _timeline_ever_in(timelines, injection_node.uniq_name, _DEAD_INFRA_STATES):
        return 0

    # Resolve dead container/pod to its owner service.
    if injection_node.kind == PlaceKind.pod:
        owner = _service_of_pod(graph, injection_node.id)
    else:
        owner = _service_of_container(graph, injection_node.id)
    if owner is None or owner.id is None:
        return 0

    consumers = _scenario_a_consumer_services(graph, injection_node)
    if not consumers:
        return 0

    g = graph._graph
    edges_added = 0
    for consumer in consumers:
        if consumer.id is None:
            continue
        # Iterate consumer's spans and only emit edges to anomalous ones.
        for _src, span_id, edge_key in g.out_edges(consumer.id, keys=True):  # type: ignore[call-arg]
            if edge_key != DepKind.includes:
                continue
            span_node = graph.get_node_by_id(span_id)
            if span_node is None or span_node.kind != PlaceKind.span:
                continue
            if not _timeline_ever_in(timelines, span_node.uniq_name, _SCENARIO_A_TARGET_SPAN_STATES):
                continue
            if _add_inferred_edge(
                graph,
                src_node=owner,
                dst_node=span_node,
                kind="depends_on_dead_infra",
            ):
                edges_added += 1
                logger.info(
                    "inferred edge added (Scenario A): %s -> %s [depends_on_dead_infra]",
                    owner.uniq_name,
                    span_node.uniq_name,
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
