"""StructuralInheritanceAdapter — containment-driven state inference."""

from __future__ import annotations

from pathlib import Path

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.structural_inheritance import (
    StructuralInheritanceAdapter,
)
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.synth import synth_timelines
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import (
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)

CTX = AdapterContext(datapack_dir=Path("/tmp/not-used"), case_name="structural-fixture")


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


def _make_timeline(node_key: str, kind: PlaceKind, state: str, *, level: EvidenceLevel) -> StateTimeline:
    return StateTimeline(
        node_key=node_key,
        kind=kind,
        windows=(
            TimelineWindow(
                start=2000,
                end=2030,
                state=state,
                level=level,
                trigger="fixture",
                evidence={},
            ),
        ),
    )


def _build_basic_graph() -> tuple[HyperGraph, dict[str, Node]]:
    """service|svc -> pod|p -> container|c, plus span|svc::GET / and span|svc::POST /."""
    g = HyperGraph()
    svc = _add_node(g, PlaceKind.service, "svc")
    pod = _add_node(g, PlaceKind.pod, "p")
    cont = _add_node(g, PlaceKind.container, "c")
    span_a = _add_node(g, PlaceKind.span, "svc::GET /")
    span_b = _add_node(g, PlaceKind.span, "svc::POST /")
    _add_edge(g, pod, cont, DepKind.runs)
    _add_edge(g, svc, pod, DepKind.routes_to)
    _add_edge(g, svc, span_a, DepKind.includes)
    _add_edge(g, svc, span_b, DepKind.includes)
    return g, {"svc": svc, "pod": pod, "cont": cont, "span_a": span_a, "span_b": span_b}


def test_container_unavailable_emits_pod_degraded() -> None:
    g, _ = _build_basic_graph()
    prior = {
        "container|c": _make_timeline("container|c", PlaceKind.container, "unavailable", level=EvidenceLevel.observed),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))
    pod_events = [e for e in events if e.node_key == "pod|p"]
    assert pod_events, f"expected pod transition, got {events}"
    assert pod_events[0].to_state == "degraded"
    assert pod_events[0].level == EvidenceLevel.inferred
    assert pod_events[0].trigger == "structural_inheritance"


def test_container_unavailable_propagates_to_service_and_spans() -> None:
    g, _ = _build_basic_graph()
    prior = {
        "container|c": _make_timeline("container|c", PlaceKind.container, "unavailable", level=EvidenceLevel.observed),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))

    svc_events = [e for e in events if e.node_key == "service|svc"]
    assert svc_events
    assert svc_events[0].to_state == "unavailable"

    span_a_events = [e for e in events if e.node_key == "span|svc::GET /"]
    span_b_events = [e for e in events if e.node_key == "span|svc::POST /"]
    assert span_a_events and span_b_events
    assert span_a_events[0].to_state == "missing"
    assert span_b_events[0].to_state == "missing"
    for e in svc_events + span_a_events + span_b_events:
        assert e.level == EvidenceLevel.inferred


def test_no_emit_when_prior_already_worse_or_equal() -> None:
    g, _ = _build_basic_graph()
    prior = {
        "container|c": _make_timeline("container|c", PlaceKind.container, "unavailable", level=EvidenceLevel.observed),
        # Service already observed at unavailable — structural would be
        # redundant; one of the spans already missing — same.
        "service|svc": _make_timeline("service|svc", PlaceKind.service, "unavailable", level=EvidenceLevel.observed),
        "span|svc::GET /": _make_timeline("span|svc::GET /", PlaceKind.span, "missing", level=EvidenceLevel.observed),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))
    # service redundant -> not emitted; span_a redundant -> not emitted
    assert not [e for e in events if e.node_key == "service|svc"]
    assert not [e for e in events if e.node_key == "span|svc::GET /"]
    # span_b had no prior -> still emitted
    assert [e for e in events if e.node_key == "span|svc::POST /"]


def test_container_erroring_cascades_to_service_only_when_pod_already_observed() -> None:
    """Strategy E semantics: container.erroring cascades pod.erroring + service.erroring.

    When the pod is already observed at erroring, the pod-step is suppressed by
    the severity check; the service-step still emits. Spans are NOT inherited
    from erroring (the application-rooted ``container_erroring_to_span`` causal
    rule walks the hop sequence on its own — this cascade only ensures pod and
    service have an intermediate window at the fault onset).
    """
    g, _ = _build_basic_graph()
    prior = {
        "container|c": _make_timeline("container|c", PlaceKind.container, "erroring", level=EvidenceLevel.observed),
        "pod|p": _make_timeline("pod|p", PlaceKind.pod, "erroring", level=EvidenceLevel.observed),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))
    pod_events = [e for e in events if e.node_key == "pod|p"]
    svc_events = [e for e in events if e.node_key == "service|svc"]
    span_events = [e for e in events if e.kind == PlaceKind.span]
    assert not pod_events, f"pod prior already erroring; cascade must suppress, got {pod_events}"
    assert svc_events and svc_events[0].to_state == "erroring"
    assert not span_events, f"erroring cascade must not propagate to spans, got {span_events}"


def test_container_slow_cascades_to_pod_degraded_and_service_slow() -> None:
    """Strategy E semantics: container.slow projects DEGRADED to pod (pod has no
    SLOW state per §11.1) and SLOW to service. Spans NOT inherited."""
    g, _ = _build_basic_graph()
    prior = {
        "container|c": _make_timeline("container|c", PlaceKind.container, "slow", level=EvidenceLevel.observed),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))
    pod_events = [e for e in events if e.node_key == "pod|p"]
    svc_events = [e for e in events if e.node_key == "service|svc"]
    span_events = [e for e in events if e.kind == PlaceKind.span]
    assert pod_events and pod_events[0].to_state == "degraded"
    assert svc_events and svc_events[0].to_state == "slow"
    assert not span_events, f"slow cascade must not propagate to spans, got {span_events}"


def test_one_container_unavailable_others_healthy_yields_pod_degraded() -> None:
    """Multi-container service convention: a single container.unavailable degrades
    the pod (matches RULE_CONTAINER_UNAVAILABLE_TO_POD) and renders the spans
    served by that pod missing. Documented convention: ANY container unavailable
    suffices to mark the pod degraded; the propagator + rules continue to handle
    fan-out from there."""
    g = HyperGraph()
    svc = _add_node(g, PlaceKind.service, "svc")
    pod = _add_node(g, PlaceKind.pod, "p")
    cont_bad = _add_node(g, PlaceKind.container, "bad")
    cont_ok = _add_node(g, PlaceKind.container, "ok")
    span = _add_node(g, PlaceKind.span, "svc::GET /")
    _add_edge(g, pod, cont_bad, DepKind.runs)
    _add_edge(g, pod, cont_ok, DepKind.runs)
    _add_edge(g, svc, pod, DepKind.routes_to)
    _add_edge(g, svc, span, DepKind.includes)

    prior = {
        "container|bad": _make_timeline(
            "container|bad", PlaceKind.container, "unavailable", level=EvidenceLevel.observed
        ),
        "container|ok": _make_timeline("container|ok", PlaceKind.container, "healthy", level=EvidenceLevel.observed),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))

    pod_events = [e for e in events if e.node_key == "pod|p"]
    svc_events = [e for e in events if e.node_key == "service|svc"]
    span_events = [e for e in events if e.node_key == "span|svc::GET /"]
    assert pod_events and pod_events[0].to_state == "degraded"
    assert svc_events and svc_events[0].to_state == "unavailable"
    assert span_events and span_events[0].to_state == "missing"


def test_empty_prior_timelines_yields_no_transitions() -> None:
    g, _ = _build_basic_graph()
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines={}).emit(CTX))
    assert events == []


def test_pod_unavailable_directly_propagates_to_service_and_spans() -> None:
    """The pod-killed path (no container observation) still propagates."""
    g, _ = _build_basic_graph()
    prior = {
        "pod|p": _make_timeline("pod|p", PlaceKind.pod, "unavailable", level=EvidenceLevel.observed),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))
    svc_events = [e for e in events if e.node_key == "service|svc"]
    span_events = [e for e in events if e.node_key.startswith("span|")]
    assert svc_events and svc_events[0].to_state == "unavailable"
    assert len(span_events) == 2
    assert all(e.to_state == "missing" for e in span_events)


def test_synth_combines_observed_and_inferred_into_single_timeline() -> None:
    """End-to-end: feeding observed transitions through phase-1 synth and the
    structural adapter through phase-2 synth produces one combined timeline
    per derived node, with the observation winning where both fired."""
    g, _ = _build_basic_graph()
    observed = [
        Transition(
            node_key="container|c",
            kind=PlaceKind.container,
            at=2000,
            from_state="healthy",
            to_state="unavailable",
            trigger="k8s.container.restarts",
            level=EvidenceLevel.observed,
            evidence={},
        ),
    ]
    phase1 = synth_timelines(observed)
    inferred = list(StructuralInheritanceAdapter(graph=g, prior_timelines=phase1).emit(CTX))
    combined = synth_timelines([*observed, *inferred])
    assert "service|svc" in combined
    assert "pod|p" in combined
    assert "span|svc::GET /" in combined
    span_states = {w.state for w in combined["span|svc::GET /"].windows}
    assert "missing" in span_states
