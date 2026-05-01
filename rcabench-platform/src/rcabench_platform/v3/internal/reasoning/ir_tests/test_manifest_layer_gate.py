"""ManifestLayerGate — magnitude-band check on each downstream node.

Feeds the canonical CPUStress manifest plus synthetic CandidatePath
shapes to verify per-edge admission of the manifest-driven layer
predicate (FORGE rework §3.2).
"""

from __future__ import annotations

from pathlib import Path

from rcabench_platform.v3.internal.reasoning.algorithms.gates.manifest_layer import (
    ManifestLayerGate,
    _edge_admitted_by_layer,
)
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath
from rcabench_platform.v3.internal.reasoning.manifests import (
    Feature,
    FeatureKind,
    ReasoningContext,
    load_manifest,
)

FIXTURE = Path(__file__).parent / "fixtures" / "cpu_stress.yaml"


def _path(node_ids: list[int], edge_descs: list[str]) -> CandidatePath:
    """Minimal CandidatePath: only ``node_ids`` and ``edge_descs`` are read by the gate."""
    n = len(node_ids)
    return CandidatePath(
        node_ids=node_ids,
        all_states=[["unknown"]] * n,
        picked_states=["unknown"] * n,
        picked_state_start_times=[0] * n,
        edge_descs=edge_descs,
        rule_ids=["test"] * (n - 1),
        rule_confidences=[1.0] * (n - 1),
        propagation_delays=[0.0] * (n - 1),
    )


def _ctx(samples: dict) -> ReasoningContext:
    return ReasoningContext(
        fault_type_name="CPUStress",
        manifest=load_manifest(FIXTURE),
        v_root_node_id=1,
        t0=0,
        feature_samples=samples,
    )


# ---------------------------------------------------------------------------
# Edge-admission helper.
# ---------------------------------------------------------------------------


def test_edge_admission_matches_parallel_pair() -> None:
    """``edge_kinds`` and ``edge_directions`` are parallel arrays."""
    layer = load_manifest(FIXTURE).derivation_layers[0]
    # CPUStress layer 1: edge_kinds=[runs, routes_to, includes],
    # edge_directions=[backward, backward, forward].
    assert _edge_admitted_by_layer("runs_BACKWARD", layer)
    assert _edge_admitted_by_layer("routes_to_BACKWARD", layer)
    assert _edge_admitted_by_layer("includes_FORWARD", layer)
    # Wrong direction for an admitted kind → reject.
    assert not _edge_admitted_by_layer("runs_FORWARD", layer)
    assert not _edge_admitted_by_layer("includes_BACKWARD", layer)
    # Unknown edge kind → reject.
    assert not _edge_admitted_by_layer("calls_FORWARD", layer)


# ---------------------------------------------------------------------------
# Layer 1: span latency or pod throttle must fall in band.
# ---------------------------------------------------------------------------


def test_layer_gate_admits_layer1_with_matching_latency() -> None:
    # Layer 1 expects span latency_p99_ratio >= 1.5 OR pod cpu_throttle_ratio >= 3.0.
    samples = {
        (2, FeatureKind.span, Feature.latency_p99_ratio): 2.5,
    }
    gate = ManifestLayerGate(_ctx(samples))
    path = _path([1, 2], ["routes_to_BACKWARD"])
    result = gate.evaluate(path, ctx=None)  # type: ignore[arg-type]
    assert result.passed, result.reason


def test_layer_gate_rejects_layer1_below_band() -> None:
    samples = {
        # Below the 1.5 lower bound — miss.
        (2, FeatureKind.span, Feature.latency_p99_ratio): 1.1,
    }
    gate = ManifestLayerGate(_ctx(samples))
    path = _path([1, 2], ["routes_to_BACKWARD"])
    result = gate.evaluate(path, ctx=None)  # type: ignore[arg-type]
    assert not result.passed
    assert "manifest layer" in result.reason


def test_layer_gate_rejects_unknown_edge_kind() -> None:
    """Edge kind not in the layer's admission set rejects even with good features."""
    samples = {
        (2, FeatureKind.span, Feature.latency_p99_ratio): 2.5,
    }
    gate = ManifestLayerGate(_ctx(samples))
    path = _path([1, 2], ["calls_FORWARD"])  # not in layer 1's edge_kinds.
    result = gate.evaluate(path, ctx=None)  # type: ignore[arg-type]
    assert not result.passed


# ---------------------------------------------------------------------------
# Multi-hop: each layer's predicate is checked independently.
# ---------------------------------------------------------------------------


def test_layer_gate_two_hop_path_admits_when_each_layer_satisfied() -> None:
    # 1 -> 2 (layer 1, routes_to_BACKWARD); 2 -> 3 (layer 2, calls_BACKWARD).
    # Layer 1 expects span latency >= 1.5; layer 2 expects span latency >= 1.2
    # OR span timeout_rate in [0.05, 1.0].
    samples = {
        (2, FeatureKind.span, Feature.latency_p99_ratio): 2.0,
        (3, FeatureKind.span, Feature.timeout_rate): 0.2,
    }
    gate = ManifestLayerGate(_ctx(samples))
    path = _path([1, 2, 3], ["routes_to_BACKWARD", "calls_BACKWARD"])
    result = gate.evaluate(path, ctx=None)  # type: ignore[arg-type]
    assert result.passed, result.reason


def test_layer_gate_two_hop_rejects_when_layer2_misses() -> None:
    samples = {
        (2, FeatureKind.span, Feature.latency_p99_ratio): 2.0,
        # Layer 2 sample missing → second edge fails.
    }
    gate = ManifestLayerGate(_ctx(samples))
    path = _path([1, 2, 3], ["routes_to_BACKWARD", "calls_BACKWARD"])
    result = gate.evaluate(path, ctx=None)  # type: ignore[arg-type]
    assert not result.passed
    failed = [e for e in result.evidence["edges"] if not e["passed"]]
    assert len(failed) == 1 and failed[0]["edge_index"] == 1


def test_layer_gate_deep_path_reuses_last_layer() -> None:
    """A path deeper than ``len(derivation_layers)`` reuses the deepest layer."""
    # CPUStress has 3 layers. A 5-node path (4 edges) is hops 1..4. Hop 4 reuses layer 3.
    samples = {
        (2, FeatureKind.span, Feature.latency_p99_ratio): 2.0,
        (3, FeatureKind.span, Feature.timeout_rate): 0.3,
        # Layer 3 expects latency >= 1.1 OR error_rate in [0.05, 1.0].
        (4, FeatureKind.span, Feature.error_rate): 0.1,
        # Beyond declared layers — reuse layer 3 envelope.
        (5, FeatureKind.span, Feature.error_rate): 0.5,
    }
    gate = ManifestLayerGate(_ctx(samples))
    path = _path(
        [1, 2, 3, 4, 5],
        [
            "routes_to_BACKWARD",  # layer 1
            "calls_BACKWARD",      # layer 2
            "calls_BACKWARD",      # layer 3
            "calls_BACKWARD",      # reuses layer 3
        ],
    )
    result = gate.evaluate(path, ctx=None)  # type: ignore[arg-type]
    assert result.passed, result.reason
    assert result.evidence["edges"][-1]["layer"] == 3


# ---------------------------------------------------------------------------
# Defensive: empty / no-manifest contexts soft-pass.
# ---------------------------------------------------------------------------


def test_layer_gate_no_manifest_soft_passes() -> None:
    gate = ManifestLayerGate(ReasoningContext())
    path = _path([1, 2], ["calls_FORWARD"])
    result = gate.evaluate(path, ctx=None)  # type: ignore[arg-type]
    assert result.passed
    assert result.evidence.get("skipped") is True
