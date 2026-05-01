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


def _name_owns(child_name: str, parent_name: str) -> bool:
    """Heuristic: ``child`` belongs to ``parent`` by uniq-name convention.

    Handles the OpenRCA conventions observed empirically:
    - service → span: span ``self_name`` = ``"<service>::<endpoint>"``
    - service → pod: pod ``self_name`` = ``"<service>-<hash>-<id>"``
    - service → container: container ``self_name`` = ``"<service>"`` (equal)
    - pod → container: container ``self_name`` = ``"<pod>"`` or starts with pod prefix
    """
    if child_name == parent_name:
        return True
    if child_name.startswith(parent_name + "::"):
        return True
    if child_name.startswith(parent_name + "-"):
        return True
    return False


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
    graph: object | None = None  # HyperGraph; typed loose to avoid cycle.

    def sample(
        self,
        node_id: int,
        kind: FeatureKind,
        feature: Feature,
    ) -> float | None:
        """Return the measured value for ``(node_id, kind, feature)`` if any."""
        return self.feature_samples.get((node_id, kind, feature))

    def aggregate_feature(
        self,
        root_id: int,
        kind: FeatureKind,
        feature: Feature,
    ) -> float | None:
        """Resolve a feature value at ``root_id``, aggregating across owned nodes.

        When the manifest's ``target_kind`` (e.g., service) differs from a
        feature's ``kind`` (e.g., span — error_rate is per-span by
        construction), a direct ``feature_samples[root_id, kind, feature]``
        lookup will miss because the extractor stores per-kind samples at
        the matching node. This helper falls back to a max-aggregation
        across nodes of ``kind`` that the graph identifies as owned by
        ``root_id`` (by uniq-name convention; see ``_name_owns``).

        Returns ``None`` if neither the direct lookup nor any owned node
        produces a sample.
        """
        direct = self.feature_samples.get((root_id, kind, feature))
        if direct is not None:
            return direct
        if self.graph is None:
            return None
        root_node = self.graph.get_node_by_id(root_id)  # type: ignore[attr-defined]
        if root_node is None:
            return None
        owned_values: list[float] = []
        for (nid, k, f), v in self.feature_samples.items():
            if k is not kind or f is not feature:
                continue
            node = self.graph.get_node_by_id(nid)  # type: ignore[attr-defined]
            if node is None:
                continue
            if _name_owns(node.self_name, root_node.self_name):
                owned_values.append(v)
        if not owned_values:
            return None
        return max(owned_values)


__all__ = ["FeatureSample", "ReasoningContext"]
