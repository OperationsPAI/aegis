"""Phase 5 — rule tier classification tests.

Verifies that:

* Every rule in ``builtin_rules.json`` has a ``tier`` field whose value is
  within :class:`RuleTier`.
* ``propagation_rules_schema.json`` validates the example file
  (``propagation_rules_example.json``) after the Phase 5 schema bump.
* ``get_builtin_rules()`` returns only ``core`` rules by default; opting
  in via ``include_augmentation=True`` returns the union of both tiers.
* The Phase 2B ``RULE_POD_DEGRADED_TO_SPAN.src_states`` cleanup landed —
  no rule still lists ``"restarting"`` because no adapter emits it.
"""

from __future__ import annotations

import json
from pathlib import Path

import jsonschema
import pytest

from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import (
    _RULES_JSON_PATH,
    get_builtin_rules,
)
from rcabench_platform.v3.internal.reasoning.rules.schema import RuleTier

_SCHEMA_PATH = Path(_RULES_JSON_PATH).parent / "propagation_rules_schema.json"
_EXAMPLE_PATH = Path(_RULES_JSON_PATH).parent / "propagation_rules_example.json"


def _load_json(path: Path) -> dict:
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def test_every_rule_in_builtin_rules_json_has_a_tier_within_enum() -> None:
    data = _load_json(_RULES_JSON_PATH)
    valid_tiers = {tier.value for tier in RuleTier}
    assert valid_tiers == {"core", "augmentation"}

    rules = data.get("rules", [])
    assert len(rules) > 0, "builtin_rules.json must define at least one rule"

    for rule in rules:
        assert "tier" in rule, f"rule {rule.get('rule_id')!r} is missing the 'tier' field"
        assert rule["tier"] in valid_tiers, (
            f"rule {rule.get('rule_id')!r} has tier={rule['tier']!r} not in {valid_tiers}"
        )


def test_schema_validates_builtin_rules_json() -> None:
    schema = _load_json(_SCHEMA_PATH)
    data = _load_json(_RULES_JSON_PATH)
    jsonschema.validate(data, schema)


def test_schema_validates_propagation_rules_example() -> None:
    schema = _load_json(_SCHEMA_PATH)
    example = _load_json(_EXAMPLE_PATH)
    jsonschema.validate(example, schema)


def test_get_builtin_rules_returns_only_core_by_default() -> None:
    rules = get_builtin_rules()
    assert len(rules) > 0
    for rule in rules:
        assert rule.tier == RuleTier.core, f"default get_builtin_rules() leaked augmentation rule {rule.rule_id!r}"


def test_get_builtin_rules_with_augmentation_is_superset_of_default() -> None:
    core = {r.rule_id for r in get_builtin_rules()}
    full = {r.rule_id for r in get_builtin_rules(include_augmentation=True)}
    assert core <= full, "include_augmentation=True must be a superset of the default"


def test_pod_degraded_to_span_no_longer_lists_restarting() -> None:
    """Phase 2B-review nit: no adapter emits pod.restarting; container restarts
    map to container.unavailable + crash_loop instead. Make sure the dead
    alternative is gone from the rule.
    """
    rules = {r.rule_id: r for r in get_builtin_rules(include_augmentation=True)}
    rule = rules.get("pod_degraded_to_span")
    assert rule is not None, "pod_degraded_to_span rule must exist"
    assert "restarting" not in rule.src_states, (
        "pod_degraded_to_span.src_states must not contain 'restarting' "
        "(no adapter emits pod.restarting; see RULE_POD_DEGRADED_TO_SPAN cleanup)."
    )


def test_phase5_required_core_rules_present() -> None:
    """Confirm the canonical-state coverage gap from Phase 5 is closed."""
    rule_ids = {r.rule_id for r in get_builtin_rules()}
    required = {
        "pod_unavailable_to_container",  # pod.UNAVAILABLE -> container.UNAVAILABLE
        "pod_unavailable_to_span",  # pod.UNAVAILABLE -> span.MISSING
        "pod_unavailable_to_service",  # pod.UNAVAILABLE -> service.UNAVAILABLE rollup
        "pod_degraded_to_span",  # pod.DEGRADED -> span.SLOW
        "container_unavailable_to_pod",  # container.UNAVAILABLE -> pod.DEGRADED
    }
    missing = required - rule_ids
    assert not missing, f"Phase 5 core coverage missing rules: {sorted(missing)}"


@pytest.mark.parametrize("include_augmentation", [False, True])
def test_get_builtin_rules_returns_a_fresh_copy(include_augmentation: bool) -> None:
    """Mutating the returned list must not corrupt the cache."""
    a = get_builtin_rules(include_augmentation=include_augmentation)
    b = get_builtin_rules(include_augmentation=include_augmentation)
    assert a is not b
    a.clear()
    fresh = get_builtin_rules(include_augmentation=include_augmentation)
    assert len(fresh) > 0
