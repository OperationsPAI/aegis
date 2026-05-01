"""ReasoningContext — runtime carrier for the active fault manifest.

Phase 1 introduced a minimal struct binding the active fault type to its
manifest. Phase 3 extends it with the per-injection runtime data the
manifest-aware gates need:

* ``v_root_node_id`` — the injection node the entry signature is checked
  against. ``None`` when the IR runner has no concrete root (sham / null
  injection cases).
* ``t0`` / ``entry_window_sec_override`` — the window
  ``[t0, t0 + entry_window_sec]`` the entry-signature check looks at.
  ``entry_window_sec_override`` lets a caller tighten the manifest's
  declared window (e.g. for unit tests); ``None`` means "use the
  manifest's own ``entry_window_sec``".
* ``feature_samples`` — pre-extracted ``(node_id, feature_kind,
  feature_name) → measured_value`` map. The manifest-aware gates do not
  themselves know how to read raw timelines / metrics — that is the
  IR adapters' job. The runner extracts the relevant features into this
  map and the gates merely test the values against the manifest bands.
* ``registry`` — ``ManifestRegistry`` reference so hand-off targets can
  be resolved without going through the global default. Useful for unit
  tests that want a sandboxed registry.

All fields are optional: a context with everything ``None`` is the
documented "no manifest, fall back to generic gates" signal.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from rcabench_platform.v3.internal.reasoning.manifests.features import (
    Feature,
    FeatureKind,
)
from rcabench_platform.v3.internal.reasoning.manifests.schema import FaultManifest

# (node_id, feature_kind, feature) → measured value (float).
FeatureSample = tuple[int, FeatureKind, Feature]


@dataclass(frozen=True, slots=True)
class ReasoningContext:
    """Per-case manifest binding handed to manifest-aware components.

    Any field can be ``None``; an entirely-empty context is the documented
    "no manifest, fall back to generic" signal. Tests construct minimal
    contexts; production code populates everything in ``cli.py`` /
    ``run_reasoning_ir`` once the runner knows the active fault type.
    """

    fault_type_name: str | None = None
    manifest: FaultManifest | None = None
    v_root_node_id: int | None = None
    t0: int | None = None
    entry_window_sec_override: int | None = None
    feature_samples: dict[FeatureSample, float] = field(default_factory=dict)
    registry: object | None = None  # ManifestRegistry; typed loose to avoid cycle.

    def sample(
        self,
        node_id: int,
        kind: FeatureKind,
        feature: Feature,
    ) -> float | None:
        """Return the measured value for ``(node_id, kind, feature)`` if any."""
        return self.feature_samples.get((node_id, kind, feature))


__all__ = ["FeatureSample", "ReasoningContext"]
