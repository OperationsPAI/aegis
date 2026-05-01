"""Smoke test for the manifest-aware gate pipeline (FORGE rework §3 acceptance).

The full Phase 4 acceptance criterion ("any 3 cases per family — each
correctly attributed when manifest exists; falls back to generic when
manifest missing") requires datapack fixtures from the 500-case
canonical dataset which do not live in-repo at unit-test scope.

This file covers the routing-level claim that the ``manifest_aware_gates``
factory and the ``ManifestRegistry`` agree on which fault types take the
manifest-driven path vs the generic fallback. Each family is sampled by
at least one manifest below; together with the per-gate tests in
``test_manifest_entry_gate.py`` and ``test_manifest_layer_gate.py``,
this gives smoke coverage of the §3.1 / §3.2 wiring.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from rcabench_platform.v3.internal.reasoning.algorithms.gates import (
    DriftGate,
    InjectTimeGate,
    ManifestEntryGate,
    ManifestLayerGate,
    TemporalGate,
    TopologyGate,
    default_gates,
    manifest_aware_gates,
)
from rcabench_platform.v3.internal.reasoning.manifests import (
    ManifestRegistry,
    ReasoningContext,
)

# 31 manifests live under src/.../manifests/fault_types/. Resolve path
# relative to this file rather than CWD so the test is location-stable.
MANIFEST_DIR = (
    Path(__file__).resolve().parents[1] / "manifests" / "fault_types"
)

# At least one fault type per family A–F (matching Phase 2 split).
SAMPLES_PER_FAMILY: dict[str, list[str]] = {
    "A_pod_lifecycle": ["PodKill", "PodFailure", "ContainerKill"],
    "B_container_resource": ["CPUStress", "MemoryStress"],
    "C_http_span": [
        "HTTPRequestAbort",
        "HTTPResponseAbort",
        "HTTPRequestDelay",
    ],
    "D_network": [
        "NetworkPartition",
        "NetworkDelay",
        "NetworkLoss",
    ],
    "E_jvm_app": [
        "JVMException",
        "JVMLatency",
        "JVMGarbageCollector",
    ],
    "F_dns_time": ["DNSError", "DNSRandom", "TimeSkew"],
}


@pytest.fixture(scope="module")
def registry() -> ManifestRegistry:
    return ManifestRegistry.from_directory(MANIFEST_DIR, strict=True)


# ---------------------------------------------------------------------------
# Routing: registry.get + manifest_aware_gates agree on path selection.
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "family,fault_types",
    list(SAMPLES_PER_FAMILY.items()),
)
def test_manifest_pipeline_selected_when_manifest_exists(
    family: str,
    fault_types: list[str],
    registry: ManifestRegistry,
) -> None:
    """For every sampled fault type, the manifest-aware path activates."""
    for ft in fault_types:
        manifest = registry.get(ft)
        assert manifest is not None, f"{family}: manifest missing for {ft!r}"
        rctx = ReasoningContext(fault_type_name=ft, manifest=manifest)
        gates = manifest_aware_gates(rctx)
        gate_names = {type(g).__name__ for g in gates}
        # Manifest gates replace TopologyGate + DriftGate; TemporalGate /
        # InjectTimeGate are preserved.
        assert "ManifestEntryGate" in gate_names
        assert "ManifestLayerGate" in gate_names
        assert "TopologyGate" not in gate_names
        assert "DriftGate" not in gate_names
        assert "TemporalGate" in gate_names
        assert "InjectTimeGate" in gate_names


def test_manifest_pipeline_falls_back_when_no_manifest() -> None:
    """An empty ReasoningContext gets the generic 4-gate stack."""
    gates = manifest_aware_gates(ReasoningContext())
    gate_types = [type(g) for g in gates]
    assert TopologyGate in gate_types
    assert DriftGate in gate_types
    assert TemporalGate in gate_types
    assert InjectTimeGate in gate_types
    assert ManifestEntryGate not in gate_types
    assert ManifestLayerGate not in gate_types


def test_manifest_pipeline_falls_back_for_unmanifested_fault_type(
    registry: ManifestRegistry,
) -> None:
    """A fault type without a manifest yields the generic stack."""
    rctx = ReasoningContext(
        fault_type_name="DefinitelyNotAFault",
        manifest=registry.get("DefinitelyNotAFault"),  # None
    )
    gates = manifest_aware_gates(rctx)
    assert [type(g) for g in gates] == [type(g) for g in default_gates()]


# ---------------------------------------------------------------------------
# Coverage: all 31 manifests load + cross-validate.
# ---------------------------------------------------------------------------


def test_all_manifests_load_and_cross_validate(registry: ManifestRegistry) -> None:
    """Sanity: the full Phase 2 corpus loads cleanly and resolves hand-offs."""
    assert len(registry) >= 30, f"expected ≥30 manifests, got {len(registry)}"
    # Every per-family sample has a loaded manifest.
    for family, fault_types in SAMPLES_PER_FAMILY.items():
        for ft in fault_types:
            assert ft in registry, f"{family}: missing manifest for {ft}"
