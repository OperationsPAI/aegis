"""Pydantic v2 models for fault manifests.

See ``docs/forge_rework/SCHEMA.md`` for the authoritative spec; this
module is the executable contract.

Validation rules implemented at model-construction time (rules 1, 2, 4,
6, 7, 8 from SCHEMA.md). Cross-manifest rules (5, hand-off targets exist)
are enforced by :class:`ManifestRegistry.cross_validate`. Rule 3
(target_kind matches injection.py) is checked at registry-load time too,
because it requires the closed fault catalog import.
"""

from __future__ import annotations

import math
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from rcabench_platform.v3.internal.reasoning.manifests.features import (
    FEATURE_METADATA,
    Feature,
    FeatureKind,
)
from rcabench_platform.v3.internal.reasoning.models.fault_seed import (
    FAULT_TYPE_TO_SEED_TIER,
    SEED_TIERS,
)

# ---------------------------------------------------------------------------
# Shared scalar types
# ---------------------------------------------------------------------------

# YAML's ``.inf`` / ``.Inf`` etc. parses to ``math.inf`` via PyYAML; allow
# ``"inf"`` / ``"-inf"`` as string fallbacks so validators are robust to
# manifest authors who write band: ["inf", "inf"] rather than [.inf, .inf].

_INF_STRINGS = {"inf", "+inf", ".inf", "infinity", "+infinity"}
_NEG_INF_STRINGS = {"-inf", "-.inf", "-infinity"}


def _coerce_float(value: Any) -> float:
    if isinstance(value, bool):  # bool is a subclass of int — exclude.
        raise TypeError("bool is not a valid band endpoint")
    if isinstance(value, int | float):
        return float(value)
    if isinstance(value, str):
        s = value.strip().lower()
        if s in _INF_STRINGS:
            return math.inf
        if s in _NEG_INF_STRINGS:
            return -math.inf
        try:
            return float(s)
        except ValueError as e:
            raise ValueError(f"cannot parse band endpoint {value!r} as float") from e
    raise TypeError(f"unsupported band endpoint type: {type(value).__name__}")


# ---------------------------------------------------------------------------
# Feature reference (used by entry / layer / hand-off triggers)
# ---------------------------------------------------------------------------


class FeatureMatch(BaseModel):
    """A (kind, feature, band, source) tuple a node must satisfy.

    ``magnitude_decay`` is only meaningful inside a derivation layer (the
    schema shows it on layer features, not entry features). Pydantic
    accepts it as Optional everywhere; entry-level validators ignore it
    rather than rejecting, because the field is harmless if present.
    """

    model_config = ConfigDict(extra="forbid", frozen=True)

    kind: FeatureKind
    feature: Feature
    band: tuple[float, float]
    magnitude_source: Literal["theoretical", "empirical"] = "theoretical"
    magnitude_decay: float | None = None

    @field_validator("band", mode="before")
    @classmethod
    def _coerce_band(cls, v: Any) -> tuple[float, float]:
        if not isinstance(v, list | tuple) or len(v) != 2:
            raise ValueError("band must be a [low, high] pair")
        lo, hi = _coerce_float(v[0]), _coerce_float(v[1])
        return (lo, hi)

    @model_validator(mode="after")
    def _check_band_order(self) -> FeatureMatch:
        # Validation rule 6: band[0] <= band[1] (with .inf handling).
        lo, hi = self.band
        if math.isnan(lo) or math.isnan(hi):
            raise ValueError("band endpoints must not be NaN")
        if lo > hi:
            raise ValueError(f"band low ({lo}) must be <= band high ({hi})")
        # Validation rule 4: feature must support kind.
        meta = FEATURE_METADATA[self.feature]
        if self.kind not in meta.kinds:
            raise ValueError(
                f"feature {self.feature.value!r} is not defined for kind "
                f"{self.kind.value!r}; supported kinds: "
                f"{sorted(k.value for k in meta.kinds)}"
            )
        return self


# ---------------------------------------------------------------------------
# Entry signature
# ---------------------------------------------------------------------------


class EntrySignature(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)

    entry_window_sec: int = Field(default=30, ge=1, le=60)
    required_features: list[FeatureMatch] = Field(default_factory=list)
    optional_features: list[FeatureMatch] = Field(default_factory=list)
    optional_min_match: int = Field(default=0, ge=0)

    @model_validator(mode="after")
    def _check_optional_min(self) -> EntrySignature:
        if self.optional_min_match > len(self.optional_features):
            raise ValueError(
                f"optional_min_match ({self.optional_min_match}) exceeds "
                f"number of optional_features ({len(self.optional_features)})"
            )
        return self


# ---------------------------------------------------------------------------
# Derivation layer
# ---------------------------------------------------------------------------

# Edge-kind / direction strings are kept as plain literals here rather
# than imported from ``models.graph`` to keep the manifests package free
# of internal coupling. Manifests that use unrecognised edge_kinds will
# fail at Phase 3 wiring time rather than schema-load time.

EdgeDirection = Literal["forward", "backward"]


class DerivationLayer(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)

    layer: int = Field(ge=1, le=5)
    edge_kinds: list[str] = Field(min_length=1)
    edge_directions: list[EdgeDirection] = Field(min_length=1)
    expected_features: list[FeatureMatch] = Field(min_length=1)
    max_fanout: int = Field(default=32, ge=1, le=1024)

    @model_validator(mode="after")
    def _check_parallel_arrays(self) -> DerivationLayer:
        if len(self.edge_kinds) != len(self.edge_directions):
            raise ValueError(
                f"edge_kinds (len {len(self.edge_kinds)}) and edge_directions "
                f"(len {len(self.edge_directions)}) must be parallel arrays"
            )
        return self


# ---------------------------------------------------------------------------
# Hand-off
# ---------------------------------------------------------------------------


class HandOffTrigger(BaseModel):
    """Quick-prefilter feature check for a hand-off transition.

    Distinct from :class:`FeatureMatch` because hand-off triggers carry a
    single ``threshold`` rather than a band — the schema example uses
    ``feature: error_rate`` + ``threshold: 0.2`` semantics ("error_rate
    >= threshold").
    """

    model_config = ConfigDict(extra="forbid", frozen=True)

    kind: FeatureKind
    feature: Feature
    threshold: float

    @model_validator(mode="after")
    def _check_feature_kind(self) -> HandOffTrigger:
        meta = FEATURE_METADATA[self.feature]
        if self.kind not in meta.kinds:
            raise ValueError(
                f"hand-off trigger feature {self.feature.value!r} not defined "
                f"for kind {self.kind.value!r}"
            )
        return self


class HandOff(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)

    to: str
    trigger: HandOffTrigger
    on_layer: int = Field(ge=1, le=5)
    rationale: str = ""


# ---------------------------------------------------------------------------
# Top-level FaultManifest
# ---------------------------------------------------------------------------

TargetKind = Literal["container", "pod", "service", "span"]


class FaultManifest(BaseModel):
    """Top-level fault manifest. One YAML per fault type."""

    model_config = ConfigDict(extra="forbid", frozen=True)

    fault_type_name: str
    target_kind: TargetKind
    seed_tier: str
    description: str = ""
    entry_signature: EntrySignature
    derivation_layers: list[DerivationLayer] = Field(min_length=1)
    hand_offs: list[HandOff] = Field(default_factory=list)
    terminals: list[FeatureMatch] = Field(default_factory=list)

    @model_validator(mode="after")
    def _check_top_level(self) -> FaultManifest:
        # Validation rule 1: fault_type_name must exist in the canonical map.
        if self.fault_type_name not in FAULT_TYPE_TO_SEED_TIER:
            raise ValueError(
                f"fault_type_name {self.fault_type_name!r} is not in "
                f"FAULT_TYPE_TO_SEED_TIER; add it to fault_seed.py first"
            )

        # Validation rule 2: seed_tier must match the canonical mapping.
        canonical = FAULT_TYPE_TO_SEED_TIER[self.fault_type_name]
        if self.seed_tier not in SEED_TIERS:
            raise ValueError(
                f"seed_tier {self.seed_tier!r} is not a valid tier; "
                f"must be one of {sorted(SEED_TIERS)}"
            )
        if self.seed_tier != canonical:
            raise ValueError(
                f"seed_tier {self.seed_tier!r} disagrees with fault_seed.py "
                f"canonical tier {canonical!r} for fault_type "
                f"{self.fault_type_name!r}"
            )

        # Validation rule 8: layers strictly increasing, max ≤ 5.
        layer_nums = [layer.layer for layer in self.derivation_layers]
        if any(b <= a for a, b in zip(layer_nums, layer_nums[1:], strict=False)):
            raise ValueError(
                f"derivation_layers must be strictly increasing; got {layer_nums}"
            )
        if max(layer_nums) > 5:
            raise ValueError(
                f"derivation_layers max layer is {max(layer_nums)}; cap is 5"
            )

        # Hand-off on_layer references must point to an existing layer.
        layer_set = set(layer_nums)
        for h in self.hand_offs:
            if h.on_layer not in layer_set:
                raise ValueError(
                    f"hand_off on_layer={h.on_layer} does not match any "
                    f"derivation_layers layer (have {sorted(layer_set)})"
                )

        return self


__all__ = [
    "DerivationLayer",
    "EdgeDirection",
    "EntrySignature",
    "FaultManifest",
    "FeatureMatch",
    "HandOff",
    "HandOffTrigger",
    "TargetKind",
]
