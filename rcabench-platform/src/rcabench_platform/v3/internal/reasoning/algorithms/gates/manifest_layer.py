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
from rcabench_platform.v3.internal.reasoning.manifests.features import (
    Feature,
    FeatureKind,
)
from rcabench_platform.v3.internal.reasoning.manifests.schema import (
    DerivationLayer,
    FeatureMatch,
)

# Mirror of ``manifest_path_builder._SLOW_TIER_REQ_COUNT_LOW``. Kept
# duplicated to avoid the gates package importing the path-builder.
_SLOW_TIER_REQ_COUNT_LOW: tuple[float, float] = (0.0, 0.7)


def _slow_tier_corroborates(
    node_id: int, rctx: ReasoningContext
) -> tuple[bool, float | None]:
    """Slow-tier corroborator: ``request_count_ratio`` low at the dst.

    Mirrors :func:`manifest_path_builder.ManifestAwarePathBuilder._slow_tier_corroborates`.
    Returns ``(matched, value)``. A missing sample fails-closed.
    """
    v = rctx.aggregate_feature(
        node_id, FeatureKind.span, Feature.request_count_ratio
    )
    if v is None:
        return False, None
    lo, hi = _SLOW_TIER_REQ_COUNT_LOW
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


def _assign_layers_from_rule_ids(
    rule_ids: list[str], layers: list[DerivationLayer]
) -> list[int | None]:
    """Map each path edge to its layer index (or None for lift edges).

    Reads the ``rule_id`` tag the path-builder stamps on each edge:

    - ``"manifest:{ft}:L{k}"`` → layer index ``k - 1`` (clamped to
      ``len(layers) - 1`` so paths deeper than the manifest reuse the
      last layer's envelope, matching SCHEMA.md's "deepest layer is the
      authoritative envelope" note).
    - ``"manifest:{ft}:lift"`` → ``None`` (skip in layer check).
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
        # Tier-driven relaxation: ``unavailable``/``silent`` faults have
        # a structurally deterministic cascade — observability gaps at
        # intermediate callers (retry-and-succeed silently, error
        # swallowed, low-traffic windows below the 5% band) are
        # downstream noise, not absence of the causal effect. The path
        # builder admits any structurally-connected dst in those tiers;
        # we mirror that here so the defensive gate doesn't reject what
        # the builder accepted. Magnitude evidence is still recorded
        # for audit. Other tiers keep strict admission.
        #
        # ``slow`` tier: cascade is observability-bounded but two-channel
        # by physics — caller either accumulates the injected delay
        # (latency p99/p50 rise) OR drops requests as its local timeout
        # / circuit-breaker chops requests against the injected delay
        # (or TCP back-pressure absorbs bandwidth-cap throughput),
        # surfacing as ``request_count_ratio`` low. The path builder
        # OR-includes this corroborator; the gate must mirror it or it
        # rejects what the builder admitted.
        relax_features = manifest.seed_tier in {"unavailable", "silent"}
        slow_tier_corroborate = manifest.seed_tier == "slow"
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

            edge_ok = _edge_admitted_by_layer(edge_desc, layer)
            features_ok, feature_evidence = _node_matches_any_expected(
                dst_id, list(layer.expected_features), rctx
            )
            slow_corroborated = False
            slow_corroborator_value: float | None = None
            if not features_ok and slow_tier_corroborate:
                slow_corroborated, slow_corroborator_value = _slow_tier_corroborates(
                    dst_id, rctx
                )
            features_admitted = features_ok or relax_features or slow_corroborated
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
                "expected_features": feature_evidence,
                "passed": edge_passed,
            }
            if slow_tier_corroborate:
                evidence_entry["slow_tier_corroborated"] = slow_corroborated
                evidence_entry["slow_tier_request_count_ratio"] = slow_corroborator_value
            edges_evidence.append(evidence_entry)

        if all_pass:
            reason = ""
        else:
            n_failed = sum(1 for e in edges_evidence if not e["passed"])
            reason = (
                f"{n_failed} edge(s) failed manifest layer check "
                f"(edge-kind or magnitude band miss)"
            )

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
