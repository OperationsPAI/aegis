"""Phase 4 of #163 — required_labels gating in rule_matcher.

Covers three scenarios:

1. A rule with non-empty ``required_labels`` does NOT match when the
   source node's timeline does not carry the required label.
2. The same rule DOES match once the label appears on the timeline.
3. A rule with empty ``required_labels`` (the default) matches whether
   labels are present or absent — backwards-compat for every core rule.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.rule_matcher import RuleMatcher
from rcabench_platform.v3.internal.reasoning.models.graph import (
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)
from rcabench_platform.v3.internal.reasoning.rules.schema import (
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
        data=None,
    )


def _two_span_graph() -> tuple[HyperGraph, int, int]:
    g = HyperGraph()
    callee = g.add_node(Node(kind=PlaceKind.span, self_name="svc-b::POST /api"))
    caller = g.add_node(Node(kind=PlaceKind.span, self_name="svc-a::GET /home"))
    assert callee.id is not None and caller.id is not None
    g.add_edge(_calls(caller.id, callee.id, caller.uniq_name, callee.uniq_name))
    return g, callee.id, caller.id


def _gc_rule() -> PropagationRule:
    return PropagationRule(
        rule_id="span_slow_gc_to_caller",
        description="frequent_gc-tagged SLOW span propagates SLOW to caller",
        tier=RuleTier.augmentation,
        src_kind=PlaceKind.span,
        src_states=["slow"],
        edge_kind=DepKind.calls,
        direction=PropagationDirection.BACKWARD,
        dst_kind=PlaceKind.span,
        possible_dst_states=["slow"],
        required_labels=frozenset({"frequent_gc"}),
    )


def _label_agnostic_rule() -> PropagationRule:
    return PropagationRule(
        rule_id="span_slow_any_to_caller",
        description="any SLOW span propagates SLOW to caller (no label gate)",
        tier=RuleTier.core,
        src_kind=PlaceKind.span,
        src_states=["slow"],
        edge_kind=DepKind.calls,
        direction=PropagationDirection.BACKWARD,
        dst_kind=PlaceKind.span,
        possible_dst_states=["slow"],
    )


def test_rule_with_required_label_does_not_fire_when_label_missing() -> None:
    g, callee_id, caller_id = _two_span_graph()
    matcher = RuleMatcher([_gc_rule()])
    matched = matcher.matches_edge(
        src_node_id=callee_id,
        dst_node_id=caller_id,
        graph=g,
        src_states={"slow"},
        dst_states={"slow"},
        is_first_hop=False,
        src_labels=frozenset(),  # no labels on src
    )
    assert matched == [], "rule with required_labels must not fire on label-free src"


def test_rule_with_required_label_fires_when_label_present() -> None:
    g, callee_id, caller_id = _two_span_graph()
    matcher = RuleMatcher([_gc_rule()])
    matched = matcher.matches_edge(
        src_node_id=callee_id,
        dst_node_id=caller_id,
        graph=g,
        src_states={"slow"},
        dst_states={"slow"},
        is_first_hop=False,
        src_labels=frozenset({"frequent_gc"}),
    )
    assert {r.rule_id for r in matched} == {"span_slow_gc_to_caller"}


def test_rule_with_empty_required_labels_is_label_agnostic() -> None:
    g, callee_id, caller_id = _two_span_graph()
    matcher = RuleMatcher([_label_agnostic_rule()])

    # No labels — must still match (backwards-compat for core rules).
    matched_no_labels = matcher.matches_edge(
        src_node_id=callee_id,
        dst_node_id=caller_id,
        graph=g,
        src_states={"slow"},
        dst_states={"slow"},
        is_first_hop=False,
    )
    assert {r.rule_id for r in matched_no_labels} == {"span_slow_any_to_caller"}

    # With labels — must also match (label-agnostic = present labels are
    # ignored, not considered a constraint to fail).
    matched_with_labels = matcher.matches_edge(
        src_node_id=callee_id,
        dst_node_id=caller_id,
        graph=g,
        src_states={"slow"},
        dst_states={"slow"},
        is_first_hop=False,
        src_labels=frozenset({"frequent_gc", "high_cpu"}),
    )
    assert {r.rule_id for r in matched_with_labels} == {"span_slow_any_to_caller"}


def test_required_labels_requires_full_subset_match() -> None:
    """If a rule requires multiple labels, partial coverage is not enough."""
    multi_label_rule = PropagationRule(
        rule_id="span_slow_gc_and_heap_to_caller",
        description="needs BOTH frequent_gc and high_heap_pressure",
        tier=RuleTier.augmentation,
        src_kind=PlaceKind.span,
        src_states=["slow"],
        edge_kind=DepKind.calls,
        direction=PropagationDirection.BACKWARD,
        dst_kind=PlaceKind.span,
        possible_dst_states=["slow"],
        required_labels=frozenset({"frequent_gc", "high_heap_pressure"}),
    )
    g, callee_id, caller_id = _two_span_graph()
    matcher = RuleMatcher([multi_label_rule])

    only_gc = matcher.matches_edge(
        src_node_id=callee_id,
        dst_node_id=caller_id,
        graph=g,
        src_states={"slow"},
        dst_states={"slow"},
        is_first_hop=False,
        src_labels=frozenset({"frequent_gc"}),
    )
    assert only_gc == []

    both = matcher.matches_edge(
        src_node_id=callee_id,
        dst_node_id=caller_id,
        graph=g,
        src_states={"slow"},
        dst_states={"slow"},
        is_first_hop=False,
        src_labels=frozenset({"frequent_gc", "high_heap_pressure", "extra"}),
    )
    assert {r.rule_id for r in both} == {"span_slow_gc_and_heap_to_caller"}


def test_default_required_labels_is_empty_frozenset() -> None:
    """Existing rule definitions stay valid: required_labels defaults to empty."""
    rule = PropagationRule(
        rule_id="legacy_rule",
        description="no required_labels declared",
        tier=RuleTier.core,
        src_kind=PlaceKind.span,
        src_states=["slow"],
        edge_kind=DepKind.calls,
        direction=PropagationDirection.BACKWARD,
        dst_kind=PlaceKind.span,
        possible_dst_states=["slow"],
    )
    assert rule.required_labels == frozenset()
