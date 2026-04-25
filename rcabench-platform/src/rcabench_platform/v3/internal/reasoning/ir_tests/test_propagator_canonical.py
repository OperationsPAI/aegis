"""FaultPropagator over canonical-state IR timelines.

End-to-end test that builds a small synthetic graph + ``StateTimeline``s
and exercises rule-based path expansion using the canonical-state vocab.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.propagator import FaultPropagator
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.models.graph import (
    CallsEdgeData,
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)
from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_rules


def _calls(src: int, dst: int, src_name: str, dst_name: str) -> Edge:
    return Edge(
        src_id=src,
        dst_id=dst,
        src_name=src_name,
        dst_name=dst_name,
        kind=DepKind.calls,
        data=CallsEdgeData(),
    )


def _includes(src: int, dst: int, src_name: str, dst_name: str) -> Edge:
    return Edge(
        src_id=src,
        dst_id=dst,
        src_name=src_name,
        dst_name=dst_name,
        kind=DepKind.includes,
        data=None,
    )


def _routes_to(src: int, dst: int, src_name: str, dst_name: str) -> Edge:
    return Edge(
        src_id=src,
        dst_id=dst,
        src_name=src_name,
        dst_name=dst_name,
        kind=DepKind.routes_to,
        data=None,
    )


def _tl(node_key: str, kind: PlaceKind, state: str, *, start: int = 1000, end: int = 2000) -> StateTimeline:
    return StateTimeline(
        node_key=node_key,
        kind=kind,
        windows=(
            TimelineWindow(
                start=start,
                end=end,
                state=state,
                level=EvidenceLevel.observed,
                trigger="test",
                evidence={},
            ),
        ),
    )


def test_propagator_finds_slow_chain_callee_to_caller() -> None:
    g = HyperGraph()
    callee = g.add_node(Node(kind=PlaceKind.span, self_name="svc-b::POST /api"))
    caller = g.add_node(Node(kind=PlaceKind.span, self_name="svc-a::GET /home"))
    assert callee.id is not None and caller.id is not None
    g.add_edge(_calls(caller.id, callee.id, caller.uniq_name, callee.uniq_name))

    timelines = {
        callee.uniq_name: _tl(callee.uniq_name, PlaceKind.span, "slow", start=1000),
        caller.uniq_name: _tl(caller.uniq_name, PlaceKind.span, "slow", start=1010),
    }

    propagator = FaultPropagator(graph=g, rules=get_builtin_rules(), timelines=timelines, max_hops=3)
    result = propagator.propagate_from_injection(
        injection_node_ids=[callee.id],
        alarm_nodes={caller.id},
    )
    assert len(result.paths) >= 1
    path = result.paths[0]
    assert path.nodes[0] == callee.id
    assert path.nodes[-1] == caller.id


def test_propagator_pod_unavailable_to_span_via_service() -> None:
    g = HyperGraph()
    pod = g.add_node(Node(kind=PlaceKind.pod, self_name="orders-0"))
    svc = g.add_node(Node(kind=PlaceKind.service, self_name="orders"))
    span = g.add_node(Node(kind=PlaceKind.span, self_name="orders::GET /list"))
    assert pod.id is not None and svc.id is not None and span.id is not None
    g.add_edge(_routes_to(svc.id, pod.id, svc.uniq_name, pod.uniq_name))
    g.add_edge(_includes(svc.id, span.id, svc.uniq_name, span.uniq_name))

    timelines = {
        pod.uniq_name: _tl(pod.uniq_name, PlaceKind.pod, "unavailable", start=1000),
        svc.uniq_name: _tl(svc.uniq_name, PlaceKind.service, "unavailable", start=1010),
        span.uniq_name: _tl(span.uniq_name, PlaceKind.span, "missing", start=1020),
    }

    propagator = FaultPropagator(graph=g, rules=get_builtin_rules(), timelines=timelines, max_hops=4)
    result = propagator.propagate_from_injection(
        injection_node_ids=[pod.id],
        alarm_nodes={span.id},
    )
    assert len(result.paths) >= 1
    leaf_states = result.paths[0].states[-1]
    assert any(s in leaf_states for s in ("missing", "unavailable", "erroring"))


def test_propagator_returns_no_paths_when_node_healthy() -> None:
    g = HyperGraph()
    callee = g.add_node(Node(kind=PlaceKind.span, self_name="svc-b::POST /api"))
    caller = g.add_node(Node(kind=PlaceKind.span, self_name="svc-a::GET /home"))
    assert callee.id is not None and caller.id is not None
    g.add_edge(_calls(caller.id, callee.id, caller.uniq_name, callee.uniq_name))

    timelines = {
        callee.uniq_name: _tl(callee.uniq_name, PlaceKind.span, "healthy", start=1000),
        caller.uniq_name: _tl(caller.uniq_name, PlaceKind.span, "healthy", start=1010),
    }

    propagator = FaultPropagator(graph=g, rules=get_builtin_rules(), timelines=timelines, max_hops=3)
    result = propagator.propagate_from_injection(
        injection_node_ids=[callee.id],
        alarm_nodes={caller.id},
    )
    assert result.paths == []


def test_propagator_erroring_chain_three_hop_span_callers() -> None:
    g = HyperGraph()
    a = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /"))
    b = g.add_node(Node(kind=PlaceKind.span, self_name="middle::POST /work"))
    c = g.add_node(Node(kind=PlaceKind.span, self_name="leaf::POST /db"))
    assert a.id is not None and b.id is not None and c.id is not None
    g.add_edge(_calls(a.id, b.id, a.uniq_name, b.uniq_name))
    g.add_edge(_calls(b.id, c.id, b.uniq_name, c.uniq_name))

    timelines = {
        c.uniq_name: _tl(c.uniq_name, PlaceKind.span, "erroring", start=1000),
        b.uniq_name: _tl(b.uniq_name, PlaceKind.span, "erroring", start=1010),
        a.uniq_name: _tl(a.uniq_name, PlaceKind.span, "erroring", start=1020),
    }

    propagator = FaultPropagator(graph=g, rules=get_builtin_rules(), timelines=timelines, max_hops=4)
    result = propagator.propagate_from_injection(
        injection_node_ids=[c.id],
        alarm_nodes={a.id},
    )
    assert len(result.paths) >= 1
    found = result.paths[0]
    assert found.nodes[0] == c.id
    assert found.nodes[-1] == a.id
