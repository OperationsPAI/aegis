"""Built-in fault propagation rules aligned with ChaosMesh fault injection.

These rules encode realistic fault propagation patterns based on ChaosMesh
injection capabilities in cloud-native environments.

Rules are loaded from builtin_rules.json. The PropagationRule model automatically
converts string values to the appropriate enum types during initialization.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, PlaceKind
from rcabench_platform.v3.internal.reasoning.rules.schema import PropagationRule

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
    """Get the cached rules dictionary."""
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


def _get_builtin_rules_list() -> list[PropagationRule]:
    """Get the ordered list of builtin rules."""
    return list(_get_rules_dict().values())


# Lazy-loaded BUILTIN_RULES for backward compatibility
# Use property-like access through module __getattr__
class _BuiltinRulesProxy:
    """Proxy object that behaves like a list but loads rules lazily."""

    def __iter__(self):
        return iter(_get_builtin_rules_list())

    def __len__(self):
        return len(_get_builtin_rules_list())

    def __getitem__(self, idx):
        return _get_builtin_rules_list()[idx]

    def copy(self):
        return _get_builtin_rules_list().copy()

    def __repr__(self):
        return repr(_get_builtin_rules_list())


BUILTIN_RULES: list[PropagationRule] = _BuiltinRulesProxy()  # type: ignore[assignment]


def get_builtin_rules() -> list[PropagationRule]:
    return _get_builtin_rules_list().copy()


def get_rules_for_edge_kind(edge_kind: DepKind) -> list[PropagationRule]:
    return [rule for rule in _get_builtin_rules_list() if rule.edge_kind == edge_kind]


def get_rules_for_place_kind(place_kind: PlaceKind, as_source: bool = True) -> list[PropagationRule]:
    if as_source:
        return [rule for rule in _get_builtin_rules_list() if rule.src_kind == place_kind]
    else:
        return [rule for rule in _get_builtin_rules_list() if rule.dst_kind == place_kind]


def visualize_builtin_rules(output_path: str | None = None, format: str = "png") -> str:
    from rcabench_platform.v3.internal.reasoning.rules.visualizer import visualize_rules

    dot = visualize_rules(_get_builtin_rules_list(), output_path=output_path, format=format, group_by_place_kind=True)
    return dot.source  # type: ignore[no-any-return]
