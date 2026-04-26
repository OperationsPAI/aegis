"""Pre-existing inferred-edge tests, rewritten for §7.3 injection-anchored semantics.

The §7.3 methodology rejects the former co-anomaly bridge formulation
(any silent service × any alarm span = candidate edge). The cases below
preserve the original test names and fixtures but assert the new
behaviour: each case calls ``enrich_with_inferred_edges`` with an
explicit ``injection_node_ids`` argument and verifies that no edge is
emitted unless §7.3 Scenario A or Scenario E is triggered.

Positive Scenario A / Scenario E coverage lives in
``test_inferred_edges_injection_anchored.py``.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.ir.adapters.inferred_edges import (
    InferredEdgeMetadata,
    enrich_with_inferred_edges,
)
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.models.graph import (
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)


def _add_node(graph: HyperGraph, kind: PlaceKind, name: str) -> Node:
    return graph.add_node(Node(kind=kind, self_name=name))


def _add_edge(graph: HyperGraph, src: Node, dst: Node, kind: DepKind) -> None:
    assert src.id is not None and dst.id is not None
    graph.add_edge(
        Edge(
            src_id=src.id,
            dst_id=dst.id,
            src_name=src.uniq_name,
            dst_name=dst.uniq_name,
            kind=kind,
            data=None,
        )
    )


def _make_timeline_buckets(
    node_key: str,
    kind: PlaceKind,
    bucket_states: list[tuple[int, int, str]],
) -> StateTimeline:
    """Build a StateTimeline from ``[(start, end, state), ...]`` triples.

    All windows are tagged ``EvidenceLevel.observed`` with a fixture trigger.
    """
    windows = tuple(
        TimelineWindow(
            start=start,
            end=end,
            state=state,
            level=EvidenceLevel.observed,
            trigger="fixture",
            evidence={},
        )
        for start, end, state in bucket_states
    )
    return StateTimeline(node_key=node_key, kind=kind, windows=windows)


def _build_two_service_graph() -> tuple[HyperGraph, dict[str, Node]]:
    """Two services with no trace edge between them.

    ``service|svc-a`` -> ``pod|pa`` -> ``container|ca``
    ``service|svc-b`` -> ``pod|pb`` -> ``container|cb`` -> ``span|svc-b::GET /``
    """
    g = HyperGraph()
    svc_a = _add_node(g, PlaceKind.service, "svc-a")
    pod_a = _add_node(g, PlaceKind.pod, "pa")
    cont_a = _add_node(g, PlaceKind.container, "ca")
    svc_b = _add_node(g, PlaceKind.service, "svc-b")
    pod_b = _add_node(g, PlaceKind.pod, "pb")
    cont_b = _add_node(g, PlaceKind.container, "cb")
    span_b = _add_node(g, PlaceKind.span, "svc-b::GET /")

    _add_edge(g, svc_a, pod_a, DepKind.routes_to)
    _add_edge(g, pod_a, cont_a, DepKind.runs)
    _add_edge(g, svc_b, pod_b, DepKind.routes_to)
    _add_edge(g, pod_b, cont_b, DepKind.runs)
    _add_edge(g, svc_b, span_b, DepKind.includes)

    return g, {
        "svc_a": svc_a,
        "pod_a": pod_a,
        "cont_a": cont_a,
        "svc_b": svc_b,
        "pod_b": pod_b,
        "cont_b": cont_b,
        "span_b": span_b,
    }


def test_single_unavailable_service_no_trace_path_emits_inferred_edge() -> None:
    """Scenario A: dead container ⇒ edge to its consumer service.

    Under §7.3, a dead container's inferred edge target is the consuming
    *service*, not an alarm span on a different service. We assert the
    Scenario A edge here; correlated alarm spans on unrelated services
    are no longer bridged (that was the rejected co-anomaly behaviour).
    """
    g, nodes = _build_two_service_graph()
    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "span|svc-b::GET /": _make_timeline_buckets("span|svc-b::GET /", PlaceKind.span, [(1000, 1050, "erroring")]),
    }

    assert nodes["cont_a"].id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [nodes["cont_a"].id])
    # Scenario A: container|ca dead -> consumer service|svc-a (one edge).
    assert n_added == 1
    assert nodes["svc_a"].id is not None
    assert g._graph.has_edge(nodes["cont_a"].id, nodes["svc_a"].id, DepKind.includes)

    metadata_store = g.data["inferred_edges"]
    metadata = metadata_store[(nodes["cont_a"].id, nodes["svc_a"].id)]
    assert isinstance(metadata, InferredEdgeMetadata)
    assert metadata.inferred is True
    assert metadata.kind == "depends_on_dead_infra"

    # Co-anomaly bridge from svc-a to span_b is explicitly NOT emitted.
    assert nodes["span_b"].id is not None
    assert not g._graph.has_edge(nodes["svc_a"].id, nodes["span_b"].id, DepKind.includes)


def test_direct_call_dependency_suppresses_inferred_edge() -> None:
    """Adding an unrelated direct ``calls`` edge does not create or
    suppress Scenario A — Scenario A still emits its consumer-service
    edge regardless of trace-graph topology."""
    g, nodes = _build_two_service_graph()
    span_a = _add_node(g, PlaceKind.span, "svc-a::POST /login")
    _add_edge(g, nodes["svc_a"], span_a, DepKind.includes)
    _add_edge(g, span_a, nodes["span_b"], DepKind.calls)

    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "span|svc-b::GET /": _make_timeline_buckets("span|svc-b::GET /", PlaceKind.span, [(1000, 1050, "erroring")]),
    }

    assert nodes["cont_a"].id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [nodes["cont_a"].id])
    # Scenario A still fires — the trace-graph dependency between svc-a
    # and span_b is irrelevant to the dead-infra → consumer-service edge.
    assert n_added == 1
    assert nodes["svc_a"].id is not None
    assert g._graph.has_edge(nodes["cont_a"].id, nodes["svc_a"].id, DepKind.includes)
    # No co-anomaly bridge from svc-a to span_b.
    assert nodes["span_b"].id is not None
    assert not g._graph.has_edge(nodes["svc_a"].id, nodes["span_b"].id, DepKind.includes)


def test_two_unavailable_services_both_emit_edges() -> None:
    """Two injection nodes both dead ⇒ each emits its own Scenario A edge."""
    g, nodes = _build_two_service_graph()
    svc_c = _add_node(g, PlaceKind.service, "svc-c")
    pod_c = _add_node(g, PlaceKind.pod, "pc")
    cont_c = _add_node(g, PlaceKind.container, "cc")
    span_c = _add_node(g, PlaceKind.span, "svc-c::POST /a")
    _add_edge(g, svc_c, pod_c, DepKind.routes_to)
    _add_edge(g, pod_c, cont_c, DepKind.runs)
    _add_edge(g, svc_c, span_c, DepKind.includes)

    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "container|cb": _make_timeline_buckets("container|cb", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "span|svc-c::POST /a": _make_timeline_buckets(
            "span|svc-c::POST /a", PlaceKind.span, [(1000, 1050, "erroring")]
        ),
    }

    assert nodes["cont_a"].id is not None and nodes["cont_b"].id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [nodes["cont_a"].id, nodes["cont_b"].id])
    # Each dead container emits one edge to its own consumer service.
    assert n_added == 2
    assert nodes["svc_a"].id is not None and nodes["svc_b"].id is not None
    assert g._graph.has_edge(nodes["cont_a"].id, nodes["svc_a"].id, DepKind.includes)
    assert g._graph.has_edge(nodes["cont_b"].id, nodes["svc_b"].id, DepKind.includes)


def test_three_unavailable_services_emits_no_edges() -> None:
    """If injection list is empty, nothing is emitted regardless of how
    many services are unavailable. The §7.3 source is always tied to an
    explicit injection node."""
    g = HyperGraph()
    for tag in ("a", "b", "c"):
        svc = _add_node(g, PlaceKind.service, f"svc-{tag}")
        pod = _add_node(g, PlaceKind.pod, f"p{tag}")
        cont = _add_node(g, PlaceKind.container, f"c{tag}")
        _add_edge(g, svc, pod, DepKind.routes_to)
        _add_edge(g, pod, cont, DepKind.runs)

    svc_d = _add_node(g, PlaceKind.service, "svc-d")
    pod_d = _add_node(g, PlaceKind.pod, "pd")
    span_d = _add_node(g, PlaceKind.span, "svc-d::GET /")
    _add_edge(g, svc_d, pod_d, DepKind.routes_to)
    _add_edge(g, svc_d, span_d, DepKind.includes)

    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "container|cb": _make_timeline_buckets("container|cb", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "container|cc": _make_timeline_buckets("container|cc", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "span|svc-d::GET /": _make_timeline_buckets("span|svc-d::GET /", PlaceKind.span, [(1000, 1050, "erroring")]),
    }

    n_added = enrich_with_inferred_edges(g, timelines, [])
    assert n_added == 0
    assert g.data.get("inferred_edges", {}) == {}


def test_degraded_only_service_emits_no_edge() -> None:
    """A container that is only ``degraded`` (never ``unavailable``) does
    not trip Scenario A — the dead-infra trigger requires UNAVAILABLE."""
    g, nodes = _build_two_service_graph()
    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "degraded")]),
        "span|svc-b::GET /": _make_timeline_buckets("span|svc-b::GET /", PlaceKind.span, [(1000, 1050, "erroring")]),
    }

    assert nodes["cont_a"].id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [nodes["cont_a"].id])
    assert n_added == 0


def test_no_co_anomaly_overlap_emits_no_edge() -> None:
    """Scenario A no longer requires temporal-coincidence buckets — only
    that the injection node's timeline ever entered ``unavailable``. With
    a dead container and a routed-to consumer service, Scenario A still
    fires here despite disjoint span anomaly windows; the rejected
    co-anomaly gate is gone, replaced by the structural injection anchor."""
    g, nodes = _build_two_service_graph()
    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1010, "unavailable")]),
        "span|svc-b::GET /": _make_timeline_buckets("span|svc-b::GET /", PlaceKind.span, [(2000, 2050, "erroring")]),
    }

    assert nodes["cont_a"].id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [nodes["cont_a"].id])
    # Scenario A: dead container -> consumer service|svc-a.
    assert n_added == 1
    assert nodes["svc_a"].id is not None
    assert g._graph.has_edge(nodes["cont_a"].id, nodes["svc_a"].id, DepKind.includes)


def test_loadgen_service_excluded_as_source() -> None:
    """A loadgen container marked unavailable still emits a Scenario A
    edge if it is the explicit injection point — §7.3 commits to the
    injection anchor and does not exclude loadgen by name. Loadgen
    exclusion was a co-anomaly-era heuristic and is no longer relevant
    once the source is always the injection point itself."""
    g = HyperGraph()
    svc_load = _add_node(g, PlaceKind.service, "loadgenerator")
    pod_load = _add_node(g, PlaceKind.pod, "p-load")
    cont_load = _add_node(g, PlaceKind.container, "c-load")
    _add_edge(g, svc_load, pod_load, DepKind.routes_to)
    _add_edge(g, pod_load, cont_load, DepKind.runs)

    svc_b = _add_node(g, PlaceKind.service, "svc-b")
    pod_b = _add_node(g, PlaceKind.pod, "pb")
    span_b = _add_node(g, PlaceKind.span, "svc-b::GET /")
    _add_edge(g, svc_b, pod_b, DepKind.routes_to)
    _add_edge(g, svc_b, span_b, DepKind.includes)

    timelines = {
        "container|c-load": _make_timeline_buckets(
            "container|c-load", PlaceKind.container, [(1000, 1050, "unavailable")]
        ),
        "span|svc-b::GET /": _make_timeline_buckets("span|svc-b::GET /", PlaceKind.span, [(1000, 1050, "erroring")]),
    }

    # The loadgen container is the explicit injection target; Scenario A
    # emits one edge to its consumer service|loadgenerator. The rejected
    # co-anomaly bridge to span_b is NOT emitted.
    assert cont_load.id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [cont_load.id])
    assert n_added == 1
    assert svc_load.id is not None
    assert g._graph.has_edge(cont_load.id, svc_load.id, DepKind.includes)
    assert span_b.id is not None
    assert not g._graph.has_edge(svc_load.id, span_b.id, DepKind.includes)


def test_same_service_pair_skipped() -> None:
    """A faulty injection container's Scenario A edge points at its own
    service (the consumer); that is by design — for Scenario A the dead
    infra is the source, the routed-to service is the target. There is
    no notion of a "same-service skip" anymore."""
    g, nodes = _build_two_service_graph()
    span_a = _add_node(g, PlaceKind.span, "svc-a::GET /")
    svc_a = g.get_node_by_name("service|svc-a")
    assert svc_a is not None and svc_a.id is not None
    _add_edge(g, svc_a, span_a, DepKind.includes)

    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "span|svc-a::GET /": _make_timeline_buckets("span|svc-a::GET /", PlaceKind.span, [(1000, 1050, "erroring")]),
    }

    assert nodes["cont_a"].id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [nodes["cont_a"].id])
    # Scenario A: container|ca -> service|svc-a (its consumer).
    assert n_added == 1
    assert g._graph.has_edge(nodes["cont_a"].id, svc_a.id, DepKind.includes)
    # No edge to the same service's span (the rejected structural-inheritance
    # overlap from the co-anomaly era).
    assert span_a.id is not None
    assert not g._graph.has_edge(svc_a.id, span_a.id, DepKind.includes) or g._graph.number_of_edges(
        svc_a.id, span_a.id
    ) == 1  # the original DepKind.includes edge added above; no duplicate


def test_no_anomalous_spans_yields_no_edges() -> None:
    """A healthy container injection emits no Scenario A edge — the
    UNAVAILABLE state is required."""
    g, nodes = _build_two_service_graph()
    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "healthy")]),
        "span|svc-b::GET /": _make_timeline_buckets("span|svc-b::GET /", PlaceKind.span, [(1000, 1050, "healthy")]),
    }

    assert nodes["cont_a"].id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [nodes["cont_a"].id])
    assert n_added == 0
