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
    DerivationLayer,
    FeatureMatch,
)


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
        value = rctx.sample(node_id, fm.kind, fm.feature)
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

        n_edges = len(path.edge_descs)
        for i in range(n_edges):
            edge_desc = path.edge_descs[i]
            dst_id = path.node_ids[i + 1]
            # Layer index is 1-indexed in the schema; cap at the last layer
            # so deep paths reuse the deepest envelope.
            layer = layers[min(i, len(layers) - 1)]

            edge_ok = _edge_admitted_by_layer(edge_desc, layer)
            features_ok, feature_evidence = _node_matches_any_expected(
                dst_id, list(layer.expected_features), rctx
            )
            edge_passed = edge_ok and features_ok
            if not edge_passed:
                all_pass = False
            edges_evidence.append(
                {
                    "edge_index": i,
                    "edge_desc": edge_desc,
                    "layer": layer.layer,
                    "dst_node_id": dst_id,
                    "edge_admitted": edge_ok,
                    "features_admitted": features_ok,
                    "expected_features": feature_evidence,
                    "passed": edge_passed,
                }
            )

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
