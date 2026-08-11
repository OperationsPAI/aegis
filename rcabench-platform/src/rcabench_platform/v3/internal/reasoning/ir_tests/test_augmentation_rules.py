"""Phase 4 of #163 — JVM augmentation rules wired through the JSON loader.

These rules opt-in via ``get_builtin_rules(include_augmentation=True)`` and
each declares ``required_labels`` so they only fire on stacks where the
JVM augmenter has tagged the source node.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_rules
from rcabench_platform.v3.internal.reasoning.rules.schema import RuleTier

EXPECTED_AUGMENTATION_RULE_IDS = {
    "pod_degraded_frequent_gc_to_span",
    "pod_degraded_high_heap_to_span",
    "container_unavailable_oom_killed",
}


def test_jvm_augmentation_rules_present_only_when_opted_in() -> None:
    core_rule_ids = {r.rule_id for r in get_builtin_rules()}
    full_rule_ids = {r.rule_id for r in get_builtin_rules(include_augmentation=True)}
    assert EXPECTED_AUGMENTATION_RULE_IDS.isdisjoint(core_rule_ids), (
        "augmentation rules must NOT leak into the default core view"
    )
    assert EXPECTED_AUGMENTATION_RULE_IDS <= full_rule_ids, (
        f"expected augmentation rules missing: {EXPECTED_AUGMENTATION_RULE_IDS - full_rule_ids}"
    )


def test_jvm_augmentation_rules_carry_required_labels() -> None:
    rules = {r.rule_id: r for r in get_builtin_rules(include_augmentation=True)}

    gc = rules["pod_degraded_frequent_gc_to_span"]
    assert gc.tier == RuleTier.augmentation
    assert gc.required_labels == frozenset({"frequent_gc"})

    heap = rules["pod_degraded_high_heap_to_span"]
    assert heap.tier == RuleTier.augmentation
    assert heap.required_labels == frozenset({"high_heap_pressure"})

    oom = rules["container_unavailable_oom_killed"]
    assert oom.tier == RuleTier.augmentation
    assert oom.required_labels == frozenset({"oom_killed"})


def test_core_rules_have_empty_required_labels() -> None:
    for rule in get_builtin_rules():
        assert rule.required_labels == frozenset(), (
            f"core rule {rule.rule_id!r} must have empty required_labels (Phase 4 invariant)"
        )
