"""ManifestEntryGate — checks v_root features against the entry signature.

This is the manifest-driven replacement for the role InjectTimeGate plays
in the generic pipeline. Where InjectTimeGate guards "did *something*
happen at the injection point in the right window?", ManifestEntryGate
guards the stronger claim "did the *fault-type-specific* signature appear
at v_root within ``entry_window_sec`` of t0?".

The gate is per-injection (not per-path): it inspects the
``ReasoningContext`` directly and ignores the candidate path. A failure
short-circuits verification of every candidate — there is no path
worth looking at if the entry signature did not fire. ``evaluate``
therefore returns the same ``GateResult`` for every path; callers
typically run the gate once and skip per-path evaluation when it fails.

Pass criterion (per SCHEMA.md "Entry signature"):

* Every ``required_features`` band matches.
* At least ``optional_min_match`` of ``optional_features`` match.

Bands are evaluated against ``reasoning_ctx.feature_samples`` — the IR
runner pre-populates this map from the relevant timelines / metrics. A
missing sample is treated as "feature did not match" (the IR adapter
knows it could not extract the feature, which is the same as the value
being out of band).
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import (
    GateContext,
    GateResult,
)
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath
from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext
from rcabench_platform.v3.internal.reasoning.manifests.features import Feature
from rcabench_platform.v3.internal.reasoning.manifests.schema import FeatureMatch


def _band_match(value: float | None, fm: FeatureMatch) -> bool:
    """Return True iff ``value`` lies in ``fm.band`` (closed ``[lo, hi]``).

    A ``None`` value (sample missing) does not match for general
    features. SCHEMA.md uses closed-interval semantics so that bands
    like ``[0.2, 1.0]`` for rate features (which cap at 1.0) match the
    natural maximum instead of silently excluding it. ``+inf`` upper
    still matches any finite value — that case stays explicit for
    readability.

    Silent-as-feature special case: when ``fm.feature == Feature.silent``
    and ``value is None``, the absence of the signal IS the silent
    signal. SCHEMA.md §3.E and the silent-tier manifests model "the
    chaos *makes* the dst go silent" by declaring ``silent`` as an
    expected feature; a destination with no timeline (the IR adapter
    extracted no observation in the abnormal window) corroborates that
    expectation directly. This mirrors the absence-as-signal predicate
    in ``manifest_path_builder._pick_dst_window``: silent admission is
    a single uniform deviation predicate rather than a per-tier
    relaxation.
    """
    if value is None:
        return fm.feature == Feature.silent
    lo, hi = fm.band
    if value < lo:
        return False
    if hi == float("inf"):
        return True
    return value <= hi


class ManifestEntryGate:
    """Verify ``v_root`` features satisfy ``manifest.entry_signature``.

    The gate consults ``reasoning_ctx`` (passed at construction) and so
    is a constant function of the candidate path. A path-by-path call
    cost is avoided in production by the manifest-aware pipeline, which
    runs the gate once and short-circuits all paths if it fails. The
    Gate protocol is preserved here so the gate can still be plugged in
    via ``evaluate_path``.
    """

    name = "manifest_entry"

    def __init__(self, reasoning_ctx: ReasoningContext) -> None:
        self._reasoning_ctx = reasoning_ctx

    @property
    def reasoning_ctx(self) -> ReasoningContext:
        return self._reasoning_ctx

    def evaluate(self, path: CandidatePath, ctx: GateContext) -> GateResult:
        del path, ctx  # gate is per-injection, not per-path
        rctx = self._reasoning_ctx
        manifest = rctx.manifest
        if manifest is None:
            # No manifest registered — this gate should not have been
            # added to the pipeline; treat as soft pass.
            return GateResult(
                gate_name=self.name,
                passed=True,
                evidence={"skipped": True, "reason": "no manifest registered"},
            )

        v_root = rctx.v_root_node_id
        if v_root is None:
            return GateResult(
                gate_name=self.name,
                passed=False,
                evidence={"reason": "v_root_node_id is None"},
                reason="entry signature requires a v_root node id",
            )

        sig = manifest.entry_signature
        required_evidence: list[dict[str, object]] = []
        all_required_pass = True
        for fm in sig.required_features:
            value = rctx.aggregate_feature(v_root, fm.kind, fm.feature)
            ok = _band_match(value, fm)
            required_evidence.append(
                {
                    "kind": fm.kind.value,
                    "feature": fm.feature.value,
                    "band": list(fm.band),
                    "value": value,
                    "matched": ok,
                }
            )
            if not ok:
                all_required_pass = False

        optional_evidence: list[dict[str, object]] = []
        optional_matched = 0
        for fm in sig.optional_features:
            value = rctx.aggregate_feature(v_root, fm.kind, fm.feature)
            ok = _band_match(value, fm)
            if ok:
                optional_matched += 1
            optional_evidence.append(
                {
                    "kind": fm.kind.value,
                    "feature": fm.feature.value,
                    "band": list(fm.band),
                    "value": value,
                    "matched": ok,
                }
            )

        optional_pass = optional_matched >= sig.optional_min_match
        passed = all_required_pass and optional_pass

        if all_required_pass and optional_pass:
            reason = ""
        elif not all_required_pass:
            n_failed = sum(1 for e in required_evidence if not e["matched"])
            reason = f"{n_failed} required feature(s) missed entry-signature band"
        else:
            reason = f"only {optional_matched}/{sig.optional_min_match} required optional features matched"

        return GateResult(
            gate_name=self.name,
            passed=passed,
            evidence={
                "fault_type_name": manifest.fault_type_name,
                "v_root_node_id": v_root,
                "required_features": required_evidence,
                "optional_features": optional_evidence,
                "optional_matched": optional_matched,
                "optional_min_match": sig.optional_min_match,
            },
            reason=reason,
        )


__all__ = ["ManifestEntryGate", "_band_match"]
