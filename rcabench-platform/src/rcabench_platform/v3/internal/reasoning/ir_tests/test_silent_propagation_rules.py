"""L5 of the canonical-state IR rollout: SILENT propagation rules.

Pins the shape of the two new core rules introduced for Class E (traffic
isolation) per §3.E of ``docs/reasoning-feature-taxonomy.md``:

* ``service_silent_to_span``  — service SILENT propagates to its spans.
* ``span_silent_to_caller``    — callee span SILENT propagates to caller.

Both rules carry a *provisional* ``confidence`` of 0.5 derived from the
Beta(2,2) prior (§11.3). The provisional flag itself rides in
``_comment`` until L7 attaches a calibrated ``P_causal`` and a
``calibration_n`` field to :class:`PropagationRule`.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, PlaceKind
from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_rules
from rcabench_platform.v3.internal.reasoning.rules.schema import PropagationDirection, RuleTier


def _find_rule(rule_id: str):
    for rule in get_builtin_rules():
        if rule.rule_id == rule_id:
            return rule
    return None


def test_service_silent_to_span_loaded() -> None:
    rule = _find_rule("service_silent_to_span")
    assert rule is not None, "service_silent_to_span rule must be loaded by get_builtin_rules()"
    assert rule.tier == RuleTier.core
    assert rule.src_kind == PlaceKind.service
    assert rule.src_states == ["silent"]
    assert rule.edge_kind == DepKind.includes
    assert rule.direction == PropagationDirection.FORWARD
    assert rule.dst_kind == PlaceKind.span
    assert set(rule.possible_dst_states) == {"silent", "missing"}
    assert rule.confidence == 0.5


def test_span_silent_to_caller_loaded() -> None:
    rule = _find_rule("span_silent_to_caller")
    assert rule is not None, "span_silent_to_caller rule must be loaded by get_builtin_rules()"
    assert rule.tier == RuleTier.core
    assert rule.src_kind == PlaceKind.span
    assert rule.src_states == ["silent"]
    assert rule.edge_kind == DepKind.calls
    assert rule.direction == PropagationDirection.FORWARD
    assert rule.dst_kind == PlaceKind.span
    assert set(rule.possible_dst_states) == {"silent", "erroring", "missing"}
    assert rule.confidence == 0.5


def test_silent_rules_are_in_core_tier() -> None:
    """Both new rules are core (no specialization labels), so they fire by default."""
    rule_ids = {r.rule_id for r in get_builtin_rules()}
    assert "service_silent_to_span" in rule_ids
    assert "span_silent_to_caller" in rule_ids


def test_silent_rules_count_increment() -> None:
    """Exactly two SILENT-source rules are introduced by L5.

    Counted by id-pattern rather than absolute total so future rule
    additions don't flake this assertion.
    """
    silent_rule_ids = {
        r.rule_id for r in get_builtin_rules() if r.rule_id in {"service_silent_to_span", "span_silent_to_caller"}
    }
    assert silent_rule_ids == {"service_silent_to_span", "span_silent_to_caller"}, (
        f"expected exactly the two L5 SILENT rules, found {silent_rule_ids}"
    )
