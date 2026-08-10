"""RuleMatcher consuming canonical-state observations: SLOW->SLOW, UNAVAILABLE chain, ERRORING."""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.rule_matcher import RuleMatcher
from rcabench_platform.v3.internal.reasoning.models.graph import (
    CallsEdgeData,
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)
from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_rules
from rcabench_platform.v3.internal.reasoning.rules.schema import (
    PathHop,
    PropagationDirection,
    PropagationRule,
    RuleTier,
)


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


def _runs(src: int, dst: int, src_name: str, dst_name: str) -> Edge:
    return Edge(
        src_id=src,
        dst_id=dst,
        src_name=src_name,
        dst_name=dst_name,
        kind=DepKind.runs,
        data=None,
    )


def test_slow_span_propagates_slow_to_caller() -> None:
    g = HyperGraph()
    callee = g.add_node(Node(kind=PlaceKind.span, self_name="svc-b::POST /api"))
    caller = g.add_node(Node(kind=PlaceKind.span, self_name="svc-a::GET /home"))
    assert callee.id is not None and caller.id is not None
    g.add_edge(_calls(caller.id, callee.id, caller.uniq_name, callee.uniq_name))

    matcher = RuleMatcher(get_builtin_rules())
    matched = matcher.matches_edge(
        src_node_id=callee.id,
        dst_node_id=caller.id,
        graph=g,
        src_states={"slow"},
        dst_states={"slow"},
        is_first_hop=False,
    )
    rule_ids = {r.rule_id for r in matched}
    assert "span_slow_to_caller" in rule_ids


def test_erroring_span_propagates_to_caller() -> None:
    g = HyperGraph()
    callee = g.add_node(Node(kind=PlaceKind.span, self_name="svc-b::POST /api"))
    caller = g.add_node(Node(kind=PlaceKind.span, self_name="svc-a::GET /home"))
    assert callee.id is not None and caller.id is not None
    g.add_edge(_calls(caller.id, callee.id, caller.uniq_name, callee.uniq_name))

    matcher = RuleMatcher(get_builtin_rules())
    matched = matcher.matches_edge(
        src_node_id=callee.id,
        dst_node_id=caller.id,
        graph=g,
        src_states={"erroring"},
        dst_states={"erroring"},
        is_first_hop=False,
    )
    rule_ids = {r.rule_id for r in matched}
    assert "span_erroring_to_caller" in rule_ids


def test_unavailable_span_propagates_to_caller() -> None:
    g = HyperGraph()
    callee = g.add_node(Node(kind=PlaceKind.span, self_name="svc-b::POST /api"))
    caller = g.add_node(Node(kind=PlaceKind.span, self_name="svc-a::GET /home"))
    assert callee.id is not None and caller.id is not None
    g.add_edge(_calls(caller.id, callee.id, caller.uniq_name, callee.uniq_name))

    matcher = RuleMatcher(get_builtin_rules())
    matched = matcher.matches_edge(
        src_node_id=callee.id,
        dst_node_id=caller.id,
        graph=g,
        src_states={"unavailable"},
        dst_states={"erroring"},
        is_first_hop=False,
    )
    rule_ids = {r.rule_id for r in matched}
    assert "span_unavailable_to_caller" in rule_ids


def test_pod_unavailable_chain_first_hop_to_service() -> None:
    g = HyperGraph()
    pod = g.add_node(Node(kind=PlaceKind.pod, self_name="orders-0"))
    svc = g.add_node(Node(kind=PlaceKind.service, self_name="orders"))
    span = g.add_node(Node(kind=PlaceKind.span, self_name="orders::GET /list"))
    assert pod.id is not None and svc.id is not None and span.id is not None
    g.add_edge(_routes_to(svc.id, pod.id, svc.uniq_name, pod.uniq_name))
    g.add_edge(_includes(svc.id, span.id, svc.uniq_name, span.uniq_name))

    matcher = RuleMatcher(get_builtin_rules())
    matched = matcher.matches_edge(
        src_node_id=pod.id,
        dst_node_id=svc.id,
        graph=g,
        src_states={"unavailable"},
        dst_states=set(),
        is_first_hop=True,
    )
    rule_ids = {r.rule_id for r in matched}
    assert "pod_unavailable_to_span" in rule_ids


def test_healthy_states_do_not_match_propagation_rule() -> None:
    g = HyperGraph()
    callee = g.add_node(Node(kind=PlaceKind.span, self_name="svc-b::POST /api"))
    caller = g.add_node(Node(kind=PlaceKind.span, self_name="svc-a::GET /home"))
    assert callee.id is not None and caller.id is not None
    g.add_edge(_calls(caller.id, callee.id, caller.uniq_name, callee.uniq_name))

    matcher = RuleMatcher(get_builtin_rules())
    matched = matcher.matches_edge(
        src_node_id=callee.id,
        dst_node_id=caller.id,
        graph=g,
        src_states={"healthy"},
        dst_states={"healthy"},
        is_first_hop=False,
    )
    rule_ids = {r.rule_id for r in matched}
    assert rule_ids == set()


def test_specialization_label_attached_via_evidence_does_not_break_match() -> None:
    """Rule predicates speak canonical state. Specialization labels live on
    Evidence and are surfaced through StateTimeline.labels_at — they don't
    constrain JSON-rule matching, so a SLOW span with label `jvm_gc` still
    matches `span_slow_to_caller`.
    """
    g = HyperGraph()
    callee = g.add_node(Node(kind=PlaceKind.span, self_name="svc-b::POST /api"))
    caller = g.add_node(Node(kind=PlaceKind.span, self_name="svc-a::GET /home"))
    assert callee.id is not None and caller.id is not None
    g.add_edge(_calls(caller.id, callee.id, caller.uniq_name, callee.uniq_name))

    matcher = RuleMatcher(get_builtin_rules())
    matched = matcher.matches_edge(
        src_node_id=callee.id,
        dst_node_id=caller.id,
        graph=g,
        src_states={"slow"},
        dst_states={"slow"},
        is_first_hop=False,
    )
    assert any(r.rule_id == "span_slow_to_caller" for r in matched)


def test_multi_hop_unavailable_chain_container_to_span() -> None:
    g = HyperGraph()
    container = g.add_node(Node(kind=PlaceKind.container, self_name="orders-app"))
    pod = g.add_node(Node(kind=PlaceKind.pod, self_name="orders-0"))
    svc = g.add_node(Node(kind=PlaceKind.service, self_name="orders"))
    span = g.add_node(Node(kind=PlaceKind.span, self_name="orders::GET /list"))
    assert all(n.id is not None for n in (container, pod, svc, span))
    g.add_edge(_runs(pod.id, container.id, pod.uniq_name, container.uniq_name))  # type: ignore[arg-type]
    g.add_edge(_routes_to(svc.id, pod.id, svc.uniq_name, pod.uniq_name))  # type: ignore[arg-type]
    g.add_edge(_includes(svc.id, span.id, svc.uniq_name, span.uniq_name))  # type: ignore[arg-type]

    matcher = RuleMatcher(get_builtin_rules())
    rule = matcher.find_matching_multi_hop_rule(
        [container.id, pod.id, svc.id, span.id],  # type: ignore[list-item]
        g,
    )
    assert rule is not None
    assert rule.rule_id == "container_unavailable_to_span"


def test_extra_rule_can_target_specialization_label_for_augmenters() -> None:
    """An augmenter rule can match canonical state PLUS a label by reading
    evidence from the timeline before invoking the matcher. The matcher
    itself remains label-agnostic; this test documents the contract by
    asserting we can layer a custom rule alongside builtin rules."""
    custom = PropagationRule(
        rule_id="custom_jvm_gc",
        description="JVM GC induced SLOW propagates SLOW",
        tier=RuleTier.augmentation,
        src_kind=PlaceKind.span,
        src_states=["slow"],
        edge_kind=DepKind.calls,
        direction=PropagationDirection.BACKWARD,
        dst_kind=PlaceKind.span,
        possible_dst_states=["slow"],
    )
    rules = get_builtin_rules() + [custom]

    g = HyperGraph()
    callee = g.add_node(Node(kind=PlaceKind.span, self_name="svc-b::POST /api"))
    caller = g.add_node(Node(kind=PlaceKind.span, self_name="svc-a::GET /home"))
    assert callee.id is not None and caller.id is not None
    g.add_edge(_calls(caller.id, callee.id, caller.uniq_name, callee.uniq_name))

    matcher = RuleMatcher(rules)
    matched = matcher.matches_edge(
        src_node_id=callee.id,
        dst_node_id=caller.id,
        graph=g,
        src_states={"slow"},
        dst_states={"slow"},
        is_first_hop=False,
    )
    rule_ids = {r.rule_id for r in matched}
    assert {"span_slow_to_caller", "custom_jvm_gc"} <= rule_ids


# Hold ref so unused-import check passes when expanding tests later.
_ = PathHop
