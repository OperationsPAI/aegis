"""Inferred call-graph edge enricher — temporal co-anomaly + infra suspicion.

Each test builds a minimal HyperGraph + StateTimeline fixture and asserts the
expected edge-mutation outcome. No parquet data is loaded.
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
    """svc-a unavailable AND span|svc-b anomalous in same windows → edge."""
    g, nodes = _build_two_service_graph()
    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "span|svc-b::GET /": _make_timeline_buckets("span|svc-b::GET /", PlaceKind.span, [(1000, 1050, "erroring")]),
    }

    n_added = enrich_with_inferred_edges(g, timelines)
    assert n_added == 1

    assert nodes["svc_a"].id is not None and nodes["span_b"].id is not None
    assert g._graph.has_edge(nodes["svc_a"].id, nodes["span_b"].id, DepKind.includes)

    metadata_store = g.data["inferred_edges"]
    metadata = metadata_store[(nodes["svc_a"].id, nodes["span_b"].id)]
    assert isinstance(metadata, InferredEdgeMetadata)
    assert metadata.inferred is True
    assert metadata.unique_infra_suspect is True
    assert metadata.co_anomaly_windows == metadata.total_anomaly_windows


def test_direct_call_dependency_suppresses_inferred_edge() -> None:
    """If svc-a has a span with a direct ``calls`` edge to span_b, no edge.

    The trace already directly observes the dependency — the propagator's
    standard rules can traverse it via ``span_*_to_caller`` rules — so an
    inferred edge would be redundant.
    """
    g, nodes = _build_two_service_graph()
    # svc-a has a span. That span has a calls edge to svc-b's span.
    span_a = _add_node(g, PlaceKind.span, "svc-a::POST /login")
    _add_edge(g, nodes["svc_a"], span_a, DepKind.includes)
    _add_edge(g, span_a, nodes["span_b"], DepKind.calls)

    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "span|svc-b::GET /": _make_timeline_buckets("span|svc-b::GET /", PlaceKind.span, [(1000, 1050, "erroring")]),
    }

    n_added = enrich_with_inferred_edges(g, timelines)
    assert n_added == 0
    assert nodes["svc_a"].id is not None and nodes["span_b"].id is not None
    assert not g._graph.has_edge(nodes["svc_a"].id, nodes["span_b"].id, DepKind.includes)


def test_two_unavailable_services_both_emit_edges() -> None:
    """Per spec, ≤ 2 unavailable services per case still produces edges
    from each — both are plausible suspects."""
    g, nodes = _build_two_service_graph()
    # Add a third service whose span we will mark anomalous.
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

    n_added = enrich_with_inferred_edges(g, timelines)
    # Both svc-a and svc-b connect to svc-c's span.
    assert n_added == 2

    assert nodes["svc_a"].id is not None and span_c.id is not None
    assert g._graph.has_edge(nodes["svc_a"].id, span_c.id, DepKind.includes)
    assert nodes["svc_b"].id is not None
    assert g._graph.has_edge(nodes["svc_b"].id, span_c.id, DepKind.includes)
    metadata_store = g.data["inferred_edges"]
    # When two services are simultaneously unavailable, neither is the
    # unique suspect.
    assert metadata_store[(nodes["svc_a"].id, span_c.id)].unique_infra_suspect is False
    assert metadata_store[(nodes["svc_b"].id, span_c.id)].unique_infra_suspect is False


def test_three_unavailable_services_emits_no_edges() -> None:
    """Uniqueness gate trips at 3+ simultaneously unavailable services —
    too many candidate causes makes the heuristic unreliable."""
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

    n_added = enrich_with_inferred_edges(g, timelines)
    assert n_added == 0
    assert g.data.get("inferred_edges", {}) == {}


def test_degraded_only_service_emits_no_edge() -> None:
    """A service that is only ``degraded`` (CPU pressure, never down) is
    NOT a candidate cause. The strict ``unavailable`` signal is what we
    need for trace-blind cases — degraded propagates noisily across
    cascade victims."""
    g, _nodes = _build_two_service_graph()
    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "degraded")]),
        "span|svc-b::GET /": _make_timeline_buckets("span|svc-b::GET /", PlaceKind.span, [(1000, 1050, "erroring")]),
    }

    n_added = enrich_with_inferred_edges(g, timelines)
    assert n_added == 0


def test_no_co_anomaly_overlap_emits_no_edge() -> None:
    """svc-a unavailable in disjoint windows from span anomaly → no edge."""
    g, _nodes = _build_two_service_graph()
    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1010, "unavailable")]),
        # Span anomaly is in a completely separate window.
        "span|svc-b::GET /": _make_timeline_buckets("span|svc-b::GET /", PlaceKind.span, [(2000, 2050, "erroring")]),
    }

    n_added = enrich_with_inferred_edges(g, timelines)
    assert n_added == 0


def test_loadgen_service_excluded_as_source() -> None:
    """Loadgen-style services emit synthetic traffic — never the cause."""
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

    n_added = enrich_with_inferred_edges(g, timelines)
    assert n_added == 0


def test_same_service_pair_skipped() -> None:
    """A faulty service should not add an inferred edge to its OWN span —
    that case is already covered by structural inheritance."""
    g, _nodes = _build_two_service_graph()
    span_a = _add_node(g, PlaceKind.span, "svc-a::GET /")
    svc_a = g.get_node_by_name("service|svc-a")
    assert svc_a is not None and svc_a.id is not None
    _add_edge(g, svc_a, span_a, DepKind.includes)

    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "span|svc-a::GET /": _make_timeline_buckets("span|svc-a::GET /", PlaceKind.span, [(1000, 1050, "erroring")]),
    }

    n_added = enrich_with_inferred_edges(g, timelines)
    assert n_added == 0


def test_no_anomalous_spans_yields_no_edges() -> None:
    """With infra faults but no anomalous spans, nothing is emitted."""
    g, _nodes = _build_two_service_graph()
    timelines = {
        "container|ca": _make_timeline_buckets("container|ca", PlaceKind.container, [(1000, 1050, "unavailable")]),
        "span|svc-b::GET /": _make_timeline_buckets("span|svc-b::GET /", PlaceKind.span, [(1000, 1050, "healthy")]),
    }

    n_added = enrich_with_inferred_edges(g, timelines)
    assert n_added == 0
