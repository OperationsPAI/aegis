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
        max_hops: int = 3,
    ) -> float | None:
        """Resolve a feature value at ``root_id``, aggregating across topologically-related nodes.

        When the manifest's ``target_kind`` (e.g., pod) differs from a
        feature's ``kind`` (e.g., span — error_rate is per-span by
        construction), a direct lookup at ``root_id`` will miss because
        the extractor stores per-kind samples on the matching node. This
        helper walks the structural-edge subgraph (``routes_to``, ``runs``,
        ``includes``, ``manages``, ``owns``) up to ``max_hops`` in either
        direction, collecting ``feature_samples`` on visited nodes whose
        kind matches ``kind``, and returns their max.

        Walks both directions because topology is not symmetric in
        edge orientation: e.g., service ``routes_to`` pod (forward), but
        from a pod root we need to reach the service via the *reverse*
        ``routes_to`` edge to then walk ``includes`` forward to spans.

        Returns ``None`` if neither the direct lookup nor any reachable
        node produces a sample.
        """
        direct = self.feature_samples.get((root_id, kind, feature))
        if direct is not None:
            return direct
        if self.graph is None:
            return None

        graph = self.graph._graph  # type: ignore[attr-defined]
        # Structural edges that carry feature aggregation semantics. Excludes
        # ``owns`` (namespace → service) because that admits sibling-service
        # traversal which would pollute aggregates across unrelated services.
        structural = {"routes_to", "runs", "includes", "manages"}

        # BFS along structural edges in both directions, up to max_hops.
        visited: set[int] = {root_id}
        frontier: list[int] = [root_id]
        values: list[float] = []
        for _ in range(max_hops):
            next_frontier: list[int] = []
            for nid in frontier:
                # outgoing edges
                for _, dst_id, _, d in graph.out_edges(nid, keys=True, data=True):
                    if dst_id in visited:
                        continue
                    edge_ref = d.get("ref")
                    if edge_ref is None or edge_ref.kind.value not in structural:
                        continue
                    visited.add(dst_id)
                    next_frontier.append(dst_id)
                    sample = self.feature_samples.get((dst_id, kind, feature))
                    if sample is not None:
                        values.append(sample)
                # incoming edges
                for src_id, _, _, d in graph.in_edges(nid, keys=True, data=True):
                    if src_id in visited:
                        continue
                    edge_ref = d.get("ref")
                    if edge_ref is None or edge_ref.kind.value not in structural:
                        continue
                    visited.add(src_id)
                    next_frontier.append(src_id)
                    sample = self.feature_samples.get((src_id, kind, feature))
                    if sample is not None:
                        values.append(sample)
            frontier = next_frontier
            if not frontier:
                break

        return max(values) if values else None


__all__ = ["FeatureSample", "ReasoningContext"]
