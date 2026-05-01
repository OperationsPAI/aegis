"""ManifestEntryGate — feature-band matching at v_root (FORGE rework §3.1).

Feeds the canonical CPUStress manifest fixture plus synthetic v_root
feature samples into the gate and asserts pass / fail per the entry
signature predicate (all required + optional_min_match optionals).
"""

from __future__ import annotations

from pathlib import Path

from rcabench_platform.v3.internal.reasoning.algorithms.gates.manifest_entry import (
    ManifestEntryGate,
)
from rcabench_platform.v3.internal.reasoning.manifests import (
    Feature,
    FeatureKind,
    ReasoningContext,
    load_manifest,
)

FIXTURE = Path(__file__).parent / "fixtures" / "cpu_stress.yaml"


def _make_ctx(samples: dict) -> ReasoningContext:
    manifest = load_manifest(FIXTURE)
    return ReasoningContext(
        fault_type_name=manifest.fault_type_name,
        manifest=manifest,
        v_root_node_id=42,
        t0=0,
        feature_samples=samples,
    )


def test_entry_gate_passes_when_required_and_optional_match() -> None:
    samples = {
        # Required: cpu_throttle_ratio in [3.0, inf) — 5.0 matches.
        (42, FeatureKind.container, Feature.cpu_throttle_ratio): 5.0,
        # Optional: thread_queue_depth in [2.0, inf) — 3.0 matches.
        (42, FeatureKind.container, Feature.thread_queue_depth): 3.0,
    }
    gate = ManifestEntryGate(_make_ctx(samples))
    result = gate.evaluate(path=None, ctx=None)  # type: ignore[arg-type]
    assert result.passed
    assert result.evidence["fault_type_name"] == "CPUStress"
    assert result.evidence["optional_matched"] >= 1


def test_entry_gate_fails_when_required_below_band() -> None:
    samples = {
        # 1.5 is below the 3.0 lower band — required miss.
        (42, FeatureKind.container, Feature.cpu_throttle_ratio): 1.5,
        (42, FeatureKind.container, Feature.thread_queue_depth): 3.0,
    }
    gate = ManifestEntryGate(_make_ctx(samples))
    result = gate.evaluate(path=None, ctx=None)  # type: ignore[arg-type]
    assert not result.passed
    assert "required" in result.reason


def test_entry_gate_fails_when_optional_min_not_met() -> None:
    # Required matches but neither optional feature is sampled.
    samples = {
        (42, FeatureKind.container, Feature.cpu_throttle_ratio): 5.0,
    }
    gate = ManifestEntryGate(_make_ctx(samples))
    result = gate.evaluate(path=None, ctx=None)  # type: ignore[arg-type]
    assert not result.passed
    assert result.evidence["optional_matched"] == 0
    assert "optional" in result.reason


def test_entry_gate_required_missing_sample_counts_as_miss() -> None:
    # No samples at all — every required feature missing → fail.
    gate = ManifestEntryGate(_make_ctx({}))
    result = gate.evaluate(path=None, ctx=None)  # type: ignore[arg-type]
    assert not result.passed
    assert all(
        not e["matched"] for e in result.evidence["required_features"]
    )


def test_entry_gate_passes_when_only_one_optional_matches() -> None:
    # CPUStress requires optional_min_match=1; one of the two optionals OK.
    samples = {
        (42, FeatureKind.container, Feature.cpu_throttle_ratio): 5.0,
        # thread_queue_depth missing.
        # latency_p99_ratio in [2.0, inf) — 2.5 matches.
        (42, FeatureKind.span, Feature.latency_p99_ratio): 2.5,
    }
    gate = ManifestEntryGate(_make_ctx(samples))
    result = gate.evaluate(path=None, ctx=None)  # type: ignore[arg-type]
    assert result.passed
    assert result.evidence["optional_matched"] == 1


def test_entry_gate_no_manifest_soft_passes() -> None:
    """Empty ReasoningContext (no manifest) returns a skipped pass.

    The factory ``manifest_aware_gates`` would not include this gate
    in that case, but the gate itself is defensive in case it gets
    reused in other pipelines.
    """
    gate = ManifestEntryGate(ReasoningContext())
    result = gate.evaluate(path=None, ctx=None)  # type: ignore[arg-type]
    assert result.passed
    assert result.evidence.get("skipped") is True


def test_entry_gate_missing_v_root_fails() -> None:
    manifest = load_manifest(FIXTURE)
    gate = ManifestEntryGate(
        ReasoningContext(
            fault_type_name=manifest.fault_type_name,
            manifest=manifest,
            v_root_node_id=None,
            feature_samples={},
        )
    )
    result = gate.evaluate(path=None, ctx=None)  # type: ignore[arg-type]
    assert not result.passed
    assert "v_root" in result.reason
