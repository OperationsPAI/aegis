"""ManifestLayerGate — per-edge check against manifest derivation layers.

Replaces the generic TopologyGate + DriftGate combination on the
manifest-aware path. For each edge ``(node_i, node_{i+1})`` at path
layer ``k`` (1-indexed):

* The edge kind + direction must appear in
  ``manifest.derivation_layers[k-1].edge_kinds`` paired with
  ``edge_directions``.
* The destination node must satisfy at least one of that layer's
  ``expected_features`` magnitude bands. The check is OR across the
  layer's expected features (one match suffices), but AND with the
  edge-kind/direction admission (both conditions must hold).

If the path is deeper than ``len(derivation_layers)``, hops past the
last declared layer reuse the last layer's predicate. This matches the
SCHEMA.md note that ``magnitude_decay`` is a "hint for verification when
sampling deeper layers" — the manifest's last layer is the
authoritative envelope for everything beyond.

Bands are read from ``ReasoningContext.feature_samples``, populated by
the IR runner. A missing sample (the adapter could not extract the
feature) counts as "did not match" — same as a value out of band.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import (
    GateContext,
    GateResult,
)
from rcabench_platform.v3.internal.reasoning.algorithms.gates.manifest_entry import _band_match
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath
from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext
from rcabench_platform.v3.internal.reasoning.manifests.schema import (
    CorroboratorConfig,
    DerivationLayer,
    FeatureMatch,
)


def _corroborator_matches(node_id: int, rctx: ReasoningContext, corr: CorroboratorConfig) -> tuple[bool, float | None]:
    """Apply ``manifest.corroborator`` to a destination.

    Mirrors :meth:`manifest_path_builder.ManifestAwarePathBuilder._corroborator_matches`.
    Returns ``(matched, value)``. A missing sample fails-closed (same
    convention as :func:`_band_match`).
    """
    v = rctx.aggregate_feature(node_id, corr.kind, corr.feature)
    if v is None:
        return False, None
    lo, hi = corr.band
    return (lo <= v <= hi), v


def _split_edge_desc(edge_desc: str) -> tuple[str, str]:
    """Parse PathBuilder's edge_desc ``"{kind}_{DIRECTION}"`` into pieces.

    PathBuilder writes the direction with its enum value, which is
    upper-case (``FORWARD`` / ``BACKWARD``). Manifest edge_directions are
    lower-case (``forward`` / ``backward``); the comparison is therefore
    done on lower-case strings.
    """
    head, _, tail = edge_desc.rpartition("_")
    return head, tail.lower()


def _edge_admitted_by_layer(edge_desc: str, layer: DerivationLayer) -> bool:
    """Return True iff the (kind, direction) pair appears in the layer.

    The schema declares ``edge_kinds`` and ``edge_directions`` as
    *parallel* arrays — index i pairs kind[i] with direction[i] — not a
    cross product. We honour that: the admission predicate is
    ``∃ i such that (kind, direction) == (edge_kinds[i], edge_directions[i])``.
    """
    kind, direction = _split_edge_desc(edge_desc)
    pairs = zip(layer.edge_kinds, layer.edge_directions, strict=False)
    for k, d in pairs:
        if k == kind and d == direction:
            return True
    return False


def _node_matches_any_expected(
    node_id: int,
    expected: list[FeatureMatch],
    rctx: ReasoningContext,
) -> tuple[bool, list[dict[str, object]]]:
    evidence: list[dict[str, object]] = []
    any_match = False
    for fm in expected:
        value = rctx.aggregate_feature(node_id, fm.kind, fm.feature)
        ok = _band_match(value, fm)
        if ok:
            any_match = True
        evidence.append(
            {
                "kind": fm.kind.value,
                "feature": fm.feature.value,
                "band": list(fm.band),
                "value": value,
                "matched": ok,
            }
        )
    return any_match, evidence


def _assign_layers_from_rule_ids(rule_ids: list[str], layers: list[DerivationLayer]) -> list[int | None]:
    """Map each path edge to its layer index (or None for lift edges).

    Reads the ``rule_id`` tag the path-builder stamps on each edge:

    - ``"manifest:{ft}:L{k}"`` → layer index ``k - 1`` (clamped to
      ``len(layers) - 1`` so paths deeper than the manifest reuse the
      last layer's envelope, matching SCHEMA.md's "deepest layer is the
      authoritative envelope" note).
    - ``"manifest:{ft}:lift"`` → ``None`` (skip in layer check).
    - ``"manifest:{ft}:Lext"`` → last layer's index (the erroring-tier
      deep-cascade extension reuses the last layer's edge_kinds as the
      structural envelope; per-edge feature admission is relaxed via the
      ``Lext`` rule_id tag handled by ``evaluate``).
    - Anything else → falls back to positional index (legacy path-builder
      output, kept for compatibility with the few tests that craft
      CandidatePaths by hand).
    """
    n = len(rule_ids)
    cap = max(len(layers) - 1, 0)
    out: list[int | None] = []
    for i, rid in enumerate(rule_ids):
        if rid.endswith(":lift"):
            out.append(None)
            continue
        if rid.endswith(":Lext"):
            out.append(cap)
            continue
        marker = rid.rpartition(":L")[2]
        if marker.isdigit():
            k = int(marker)
            out.append(min(k - 1, cap) if k >= 1 else 0)
        else:
            out.append(min(i, cap))
    # Pad in case rule_ids is shorter than edge_descs (defensive).
    while len(out) < n:
        out.append(min(len(out), cap))
    return out


class ManifestLayerGate:
    """Per-edge magnitude-band + edge-kind admission for manifest paths."""

    name = "manifest_layer"

    def __init__(self, reasoning_ctx: ReasoningContext) -> None:
        self._reasoning_ctx = reasoning_ctx

    @property
    def reasoning_ctx(self) -> ReasoningContext:
        return self._reasoning_ctx

    def evaluate(self, path: CandidatePath, ctx: GateContext) -> GateResult:
        del ctx  # gate is path-only; manifest comes from reasoning_ctx
        rctx = self._reasoning_ctx
        manifest = rctx.manifest
        if manifest is None:
            return GateResult(
                gate_name=self.name,
                passed=True,
                evidence={"skipped": True, "reason": "no manifest registered"},
            )

        layers = manifest.derivation_layers
        if not layers:
            return GateResult(
                gate_name=self.name,
                passed=True,
                evidence={"skipped": True, "reason": "manifest has no derivation layers"},
            )

        edges_evidence: list[dict[str, object]] = []
        all_pass = True

        # Layer assignment is read from the path-builder's ``rule_ids``
        # tagging convention rather than positional index. The
        # ``ManifestAwarePathBuilder`` writes:
        #   - ``manifest:{ft}:L{k}``  for normal layer-k admits
        #   - ``manifest:{ft}:lift``  for structural transit edges that
        #     bridge plane gaps between layer-k and layer-(k+1)
        # Lift edges are not part of any layer's contract — they exist
        # only to walk between planes (e.g., service→its-owned-span via
        # ``includes`` so a layer-2 ``calls`` admission can fire). We
        # skip them here. Without this skip, a positional ``layer[i]``
        # mapping forces the lift edge into layer-(k+1)'s edge_kinds
        # check, where it always fails — silently rejecting the entire
        # cascade for any infra-fault manifest whose v_root sits on a
        # different plane than its layer-2 edge_kinds (PodFailure,
        # ContainerKill, CPUStress, MemoryStress, JVMMemoryStress, etc.).
        # Uniform deviation predicate (paper §3.3 condition (i)).
        # ``_band_match`` handles the silent-as-feature special case
        # (None value matches when feature == silent), so silent-tier
        # cascades admit through the same band check as every other
        # tier — no per-tier global relaxation here. ``manifest.corroborator``
        # adds a parallel OR-channel evaluated when the layer's bands
        # all miss (slow tier uses request_count_ratio for the
        # throughput-drop channel). Erroring extension edges (rule_id
        # ``:Lext``) are still per-edge relaxed in the loop below.
        corroborator = manifest.corroborator
        n_edges = len(path.edge_descs)
        layer_indices = _assign_layers_from_rule_ids(path.rule_ids, layers)
        for i in range(n_edges):
            edge_desc = path.edge_descs[i]
            dst_id = path.node_ids[i + 1]
            layer_idx = layer_indices[i]
            if layer_idx is None:
                # Lift edge: structural bridge, skip the layer-bound
                # check. The dst is a plane-aligned proxy whose own
                # admissibility is verified by the next non-lift edge.
                edges_evidence.append(
                    {
                        "edge_index": i,
                        "edge_desc": edge_desc,
                        "layer": None,
                        "dst_node_id": dst_id,
                        "edge_admitted": True,
                        "features_admitted": True,
                        "expected_features": [],
                        "passed": True,
                        "skipped": "lift",
                    }
                )
                continue
            layer = layers[layer_idx]

            # Per-edge tier-relax: extension edges that the path builder
            # admitted with ``relaxed_features=True`` (erroring-tier
            # past-the-anchor extension) carry ``per_edge_relaxed[i] ==
            # True``. The gate honours that bool rather than parsing the
            # rule_id string — rule_ids stay audit-only metadata
            # (``manifest:{ft}:Lext`` is still the canonical tag).
            is_extension_edge = i < len(path.per_edge_relaxed) and path.per_edge_relaxed[i]
            relax_features = is_extension_edge

            edge_ok = _edge_admitted_by_layer(edge_desc, layer)
            features_ok, feature_evidence = _node_matches_any_expected(dst_id, list(layer.expected_features), rctx)
            corroborated = False
            corroborator_value: float | None = None
            if not features_ok and corroborator is not None:
                corroborated, corroborator_value = _corroborator_matches(dst_id, rctx, corroborator)
            features_admitted = features_ok or relax_features or corroborated
            edge_passed = edge_ok and features_admitted
            if not edge_passed:
                all_pass = False
            evidence_entry: dict[str, object] = {
                "edge_index": i,
                "edge_desc": edge_desc,
                "layer": layer.layer,
                "dst_node_id": dst_id,
                "edge_admitted": edge_ok,
                "features_admitted": features_admitted,
                "features_match_band": features_ok,
                "tier_relaxed": relax_features and not features_ok,
                "tier_relax_source": "extension" if is_extension_edge else None,
                "expected_features": feature_evidence,
                "passed": edge_passed,
            }
            if corroborator is not None:
                evidence_entry["corroborated"] = corroborated
                evidence_entry["corroborator_value"] = corroborator_value
            edges_evidence.append(evidence_entry)

        if all_pass:
            reason = ""
        else:
            n_failed = sum(1 for e in edges_evidence if not e["passed"])
            reason = f"{n_failed} edge(s) failed manifest layer check (edge-kind or magnitude band miss)"

        return GateResult(
            gate_name=self.name,
            passed=all_pass,
            evidence={
                "fault_type_name": manifest.fault_type_name,
                "edges": edges_evidence,
            },
            reason=reason,
        )


__all__ = [
    "ManifestLayerGate",
    "_edge_admitted_by_layer",
    "_node_matches_any_expected",
]
