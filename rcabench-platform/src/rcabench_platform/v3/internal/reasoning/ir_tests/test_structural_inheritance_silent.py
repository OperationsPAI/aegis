"""StructuralInheritanceAdapter — Class E ``service.silent`` cascade tests.

These tests cover the cascade-aggregation piece for Class E (traffic
isolation): ``service.silent`` should inherit down to every owned span as
``span.silent``, mirroring the existing ``service.unavailable -> span.missing``
rule. This file is kept separate from ``test_structural_inheritance_adapter``
so the silent path is reviewable in isolation; existing service.unavailable
behaviour is exercised once here as a regression guard for the parameter
default refactor.
"""

from __future__ import annotations

from pathlib import Path

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.structural_inheritance import (
    StructuralInheritanceAdapter,
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

CTX = AdapterContext(datapack_dir=Path("/tmp/not-used"), case_name="structural-silent-fixture")


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
    """service|svc-A -> pod|p -> container|c1, plus span|svc-A::ep1 / span|svc-A::ep2."""
    g = HyperGraph()
    svc = _add_node(g, PlaceKind.service, "svc-A")
    pod = _add_node(g, PlaceKind.pod, "p")
    cont = _add_node(g, PlaceKind.container, "c1")
    span_a = _add_node(g, PlaceKind.span, "svc-A::ep1")
    span_b = _add_node(g, PlaceKind.span, "svc-A::ep2")
    _add_edge(g, pod, cont, DepKind.runs)
    _add_edge(g, svc, pod, DepKind.routes_to)
    _add_edge(g, svc, span_a, DepKind.includes)
    _add_edge(g, svc, span_b, DepKind.includes)
    return g, {"svc": svc, "pod": pod, "cont": cont, "span_a": span_a, "span_b": span_b}


def test_service_silent_propagates_to_each_span() -> None:
    g, _ = _build_basic_graph()
    prior = {
        "service|svc-A": _make_timeline(
            "service|svc-A", PlaceKind.service, "silent", level=EvidenceLevel.observed
        ),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))

    span_keys = {e.node_key for e in events if e.node_key.startswith("span|")}
    assert span_keys == {"span|svc-A::ep1", "span|svc-A::ep2"}, f"got {span_keys}"
    span_events = [e for e in events if e.node_key.startswith("span|")]
    assert len(span_events) == 2
    assert all(e.to_state == "silent" for e in span_events)


def test_service_silent_emits_silent_not_missing() -> None:
    g, _ = _build_basic_graph()
    prior = {
        "service|svc-A": _make_timeline(
            "service|svc-A", PlaceKind.service, "silent", level=EvidenceLevel.observed
        ),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))

    span_events = [e for e in events if e.node_key.startswith("span|")]
    assert span_events, "expected at least one span event"
    for ev in span_events:
        assert ev.to_state == "silent", f"silent service must not produce 'missing', got {ev}"
        assert ev.to_state != "missing"


def test_service_unavailable_still_propagates_to_missing() -> None:
    g, _ = _build_basic_graph()
    prior = {
        "service|svc-A": _make_timeline(
            "service|svc-A", PlaceKind.service, "unavailable", level=EvidenceLevel.observed
        ),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))

    span_events = [e for e in events if e.node_key.startswith("span|")]
    assert len(span_events) == 2
    assert all(e.to_state == "missing" for e in span_events)


def test_silent_inheritance_is_inferred_level() -> None:
    g, _ = _build_basic_graph()
    prior = {
        "service|svc-A": _make_timeline(
            "service|svc-A", PlaceKind.service, "silent", level=EvidenceLevel.observed
        ),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))
    span_events = [e for e in events if e.node_key.startswith("span|")]
    assert span_events
    for ev in span_events:
        assert ev.level == EvidenceLevel.inferred, f"expected inferred, got {ev.level} on {ev}"


def test_silent_inheritance_suppressed_when_observed_present() -> None:
    g, _ = _build_basic_graph()
    prior = {
        "service|svc-A": _make_timeline(
            "service|svc-A", PlaceKind.service, "silent", level=EvidenceLevel.observed
        ),
        # ep1 already observed silent — structural claim would be redundant
        # (same severity), so _is_weaker_or_equal must suppress it.
        "span|svc-A::ep1": _make_timeline(
            "span|svc-A::ep1", PlaceKind.span, "silent", level=EvidenceLevel.observed
        ),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))

    ep1 = [e for e in events if e.node_key == "span|svc-A::ep1"]
    ep2 = [e for e in events if e.node_key == "span|svc-A::ep2"]
    assert ep1 == [], f"ep1 already observed silent — must not be re-emitted, got {ep1}"
    assert ep2, "ep2 had no prior observation — should still be emitted"
    assert ep2[0].to_state == "silent"


def test_no_silent_inheritance_for_pod_or_container_silent() -> None:
    """Regression guard: even if a future bug parks SILENT on PodStateIR /
    ContainerStateIR, this adapter must not propagate it. Per §11.1 only
    request-layer kinds carry SILENT."""
    g, _ = _build_basic_graph()
    # Synthetic — PodStateIR / ContainerStateIR have no SILENT today; we
    # construct the timelines by hand to assert the adapter's structural
    # path is locked down.
    prior = {
        "pod|p": _make_timeline("pod|p", PlaceKind.pod, "silent", level=EvidenceLevel.observed),
        "container|c1": _make_timeline(
            "container|c1", PlaceKind.container, "silent", level=EvidenceLevel.observed
        ),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))
    assert events == [], (
        "pod/container silent must not propagate via structural inheritance, "
        f"got {events}"
    )


def test_existing_container_unavailable_to_span_missing_unchanged() -> None:
    """Smoke test for the parameter-default refactor: container.unavailable
    must still produce pod.degraded + service.unavailable + span.missing for
    every owned span, with no behavioural change."""
    g, _ = _build_basic_graph()
    prior = {
        "container|c1": _make_timeline(
            "container|c1", PlaceKind.container, "unavailable", level=EvidenceLevel.observed
        ),
    }
    events = list(StructuralInheritanceAdapter(graph=g, prior_timelines=prior).emit(CTX))

    pod_events = [e for e in events if e.node_key == "pod|p"]
    svc_events = [e for e in events if e.node_key == "service|svc-A"]
    span_events = [e for e in events if e.node_key.startswith("span|")]

    assert len(pod_events) == 1 and pod_events[0].to_state == "degraded"
    assert len(svc_events) == 1 and svc_events[0].to_state == "unavailable"
    assert len(span_events) == 2
    assert {e.to_state for e in span_events} == {"missing"}
