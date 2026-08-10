"""Built-in fault propagation rules aligned with ChaosMesh fault injection.

These rules encode realistic fault propagation patterns based on ChaosMesh
injection capabilities in cloud-native environments.

Rules are loaded from ``builtin_rules.json``. The :class:`PropagationRule`
model automatically converts string values to the appropriate enum types
during initialization.

Each rule carries a :class:`RuleTier` classification:

* ``core`` rules speak only canonical IR states and fire on any
  OTel-instrumented stack out-of-the-box. They are returned by
  :func:`get_builtin_rules` by default.
* ``augmentation`` rules depend on specialization labels emitted by
  specific augmenter adapters. They are skipped by default and must be
  opted-in via ``get_builtin_rules(include_augmentation=True)``.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, PlaceKind
from rcabench_platform.v3.internal.reasoning.rules.schema import (
    PropagationRule,
    RuleTier,
    StructuralRule,
)

# ==============================================================================
# JSON Rules Loading
# ==============================================================================

# JSON files are co-located with this module.
_RULES_JSON_PATH = Path(__file__).parent / "builtin_rules.json"
_rules_cache: dict[str, PropagationRule] | None = None


def _load_rules_from_json() -> dict[str, PropagationRule]:
    """Load rules from JSON file and return as dict keyed by rule name."""
    global _rules_cache
    if _rules_cache is not None:
        return _rules_cache

    with open(_RULES_JSON_PATH, encoding="utf-8") as f:
        data = json.load(f)

    rules: dict[str, PropagationRule] = {}
    for rule_data in data.get("rules", []):
        # Skip comment-only entries
        if "_comment" in rule_data and len(rule_data) == 1:
            continue

        rule_name = rule_data.pop("name", None)
        if rule_name is None:
            rule_name = f"RULE_{rule_data.get('rule_id', 'unknown').upper()}"

        # Remove fields not in PropagationRule model
        rule_data.pop("_comment", None)

        # Convert state strings to lowercase to match enum values
        # Original code used enums like SpanState.HIGH_AVG_LATENCY whose .value is 'high_avg_latency'
        if "src_states" in rule_data:
            rule_data["src_states"] = [s.lower() for s in rule_data["src_states"]]
        if "possible_dst_states" in rule_data:
            rule_data["possible_dst_states"] = [s.lower() for s in rule_data["possible_dst_states"]]

        # Create PropagationRule - validators handle string-to-enum conversion
        rule = PropagationRule(**rule_data)
        rules[rule_name] = rule

    _rules_cache = rules
    return rules


def _get_rules_dict() -> dict[str, PropagationRule]:
    """Get the cached rules dictionary (every rule, both tiers)."""
    return _load_rules_from_json()


# ==============================================================================
# Dynamic Module Attributes for Backward Compatibility
# ==============================================================================
# This allows: from builtin_rules import RULE_POD_KILL_TO_CONTAINER


def __getattr__(name: str) -> Any:
    """Dynamically provide rule variables like RULE_POD_KILL_TO_CONTAINER."""
    if name.startswith("RULE_"):
        rules = _get_rules_dict()
        if name in rules:
            return rules[name]
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")


def __dir__() -> list[str]:
    """List available module attributes including dynamic rule names."""
    base = [
        "BUILTIN_RULES",
        "get_builtin_rules",
        "get_rules_for_edge_kind",
        "get_rules_for_place_kind",
        "visualize_builtin_rules",
    ]
    rules = _get_rules_dict()
    return base + list(rules.keys())


# ==============================================================================
# Rule Database
# ==============================================================================


def _get_all_rules_list() -> list[PropagationRule]:
    """Get the ordered list of every loaded rule (both tiers)."""
    return list(_get_rules_dict().values())


def _get_core_rules_list() -> list[PropagationRule]:
    """Get only ``core`` tier rules."""
    return [rule for rule in _get_all_rules_list() if rule.tier == RuleTier.core]


# Lazy-loaded BUILTIN_RULES for backward compatibility
# Use property-like access through module __getattr__
class _BuiltinRulesProxy:
    """Proxy object that behaves like a list but loads rules lazily.

    Returns *core* rules to match ``get_builtin_rules()`` default semantics.
    """

    def __iter__(self):
        return iter(_get_core_rules_list())

    def __len__(self):
        return len(_get_core_rules_list())

    def __getitem__(self, idx):
        return _get_core_rules_list()[idx]

    def copy(self):
        return _get_core_rules_list().copy()

    def __repr__(self):
        return repr(_get_core_rules_list())


BUILTIN_RULES: list[PropagationRule] = _BuiltinRulesProxy()  # type: ignore[assignment]


def get_builtin_rules(*, include_augmentation: bool = False) -> list[PropagationRule]:
    """Return built-in propagation rules.

    Args:
        include_augmentation: If ``False`` (default), return only ``core``
            rules — those whose predicates speak the canonical IR state
            vocabulary and therefore fire on any OTel-instrumented stack.
            If ``True``, also include ``augmentation`` rules that depend
            on specialization labels emitted by specific augmenter
            adapters (e.g. JVM, OOM augmenters).
    """
    if include_augmentation:
        return _get_all_rules_list().copy()
    return _get_core_rules_list().copy()


def get_rules_for_edge_kind(edge_kind: DepKind, *, include_augmentation: bool = False) -> list[PropagationRule]:
    rules = get_builtin_rules(include_augmentation=include_augmentation)
    return [rule for rule in rules if rule.edge_kind == edge_kind]


def get_rules_for_place_kind(
    place_kind: PlaceKind,
    as_source: bool = True,
    *,
    include_augmentation: bool = False,
) -> list[PropagationRule]:
    rules = get_builtin_rules(include_augmentation=include_augmentation)
    if as_source:
        return [rule for rule in rules if rule.src_kind == place_kind]
    else:
        return [rule for rule in rules if rule.dst_kind == place_kind]


# ==============================================================================
# Structural Rules Loading (containment-axis IR-layer cascades)
# ==============================================================================

_STRUCTURAL_RULES_JSON_PATH = Path(__file__).parent / "structural_rules.json"
_structural_rules_cache: list[StructuralRule] | None = None


def _load_structural_rules_from_json() -> list[StructuralRule]:
    global _structural_rules_cache
    if _structural_rules_cache is not None:
        return _structural_rules_cache

    with open(_STRUCTURAL_RULES_JSON_PATH, encoding="utf-8") as f:
        data = json.load(f)

    rules: list[StructuralRule] = []
    for rule_data in data.get("rules", []):
        rules.append(StructuralRule(**rule_data))
    _structural_rules_cache = rules
    return rules


def get_builtin_structural_rules() -> list[StructuralRule]:
    """Return built-in structural cascade rules.

    Structural rules are class-A axioms asserted at the IR layer by
    ``StructuralInheritanceAdapter`` before the propagator runs. They are
    not subject to the 4-gate falsification (the gates apply to causal
    propagation, not topology containment).
    """
    return list(_load_structural_rules_from_json())
