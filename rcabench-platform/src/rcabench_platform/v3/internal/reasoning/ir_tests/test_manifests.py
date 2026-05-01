"""Unit tests for the FORGE-rework manifest plumbing (Phase 1)."""

from __future__ import annotations

from pathlib import Path
from textwrap import dedent

import pytest

from rcabench_platform.v3.internal.reasoning.manifests import (
    FaultManifest,
    ManifestLoadError,
    ManifestRegistry,
    load_manifest,
)
from rcabench_platform.v3.internal.reasoning.manifests.loader import _load_yaml

FIXTURE_DIR = Path(__file__).parent / "fixtures"
CPU_STRESS_FIXTURE = FIXTURE_DIR / "cpu_stress.yaml"


# ---------------------------------------------------------------------------
# 1. Load the canonical worked example end-to-end.
# ---------------------------------------------------------------------------


def test_load_valid_manifest() -> None:
    manifest = load_manifest(CPU_STRESS_FIXTURE)
    assert isinstance(manifest, FaultManifest)
    assert manifest.fault_type_name == "CPUStress"
    assert manifest.target_kind == "container"
    assert manifest.seed_tier == "degraded"
    assert manifest.entry_signature.entry_window_sec == 30
    assert len(manifest.entry_signature.required_features) == 1
    assert len(manifest.derivation_layers) == 3
    assert [layer.layer for layer in manifest.derivation_layers] == [1, 2, 3]
    assert len(manifest.hand_offs) == 1
    assert manifest.hand_offs[0].to == "HTTPResponseAbort"


# ---------------------------------------------------------------------------
# 2. Validation rule 1: fault_type_name must exist in FAULT_TYPE_TO_SEED_TIER.
# ---------------------------------------------------------------------------


def _write(tmp_path: Path, body: str, name: str = "m.yaml") -> Path:
    target = tmp_path / name
    target.write_text(dedent(body))
    return target


def _minimal_yaml(
    fault_type_name: str = "CPUStress",
    target_kind: str = "container",
    seed_tier: str = "degraded",
) -> str:
    return f"""\
        fault_type_name: {fault_type_name}
        target_kind: {target_kind}
        seed_tier: {seed_tier}
        description: minimal
        entry_signature:
          entry_window_sec: 30
          required_features:
            - kind: container
              feature: cpu_throttle_ratio
              band: [3.0, .inf]
              magnitude_source: theoretical
          optional_features: []
          optional_min_match: 0
        derivation_layers:
          - layer: 1
            edge_kinds: [includes]
            edge_directions: [forward]
            expected_features:
              - kind: span
                feature: latency_p99_ratio
                band: [1.5, .inf]
                magnitude_decay: 0.7
            max_fanout: 32
        hand_offs: []
        terminals: []
        """


def test_reject_unknown_fault_type_name(tmp_path: Path) -> None:
    yaml = _minimal_yaml(fault_type_name="DefinitelyNotARealFault")
    p = _write(tmp_path, yaml)
    with pytest.raises(ManifestLoadError) as exc:
        load_manifest(p)
    assert "FAULT_TYPE_TO_SEED_TIER" in exc.value.detail


# ---------------------------------------------------------------------------
# 3. Validation rule 2: seed_tier must match the canonical mapping.
# ---------------------------------------------------------------------------


def test_reject_seed_tier_mismatch(tmp_path: Path) -> None:
    # CPUStress is degraded in fault_seed.py; supplying "slow" must fail.
    yaml = _minimal_yaml(seed_tier="slow")
    p = _write(tmp_path, yaml)
    with pytest.raises(ManifestLoadError) as exc:
        load_manifest(p)
    assert "seed_tier" in exc.value.detail
    assert "degraded" in exc.value.detail


# ---------------------------------------------------------------------------
# 4. Validation rule 4: feature must exist in features.py.
# ---------------------------------------------------------------------------


def test_reject_unknown_feature(tmp_path: Path) -> None:
    yaml = """\
        fault_type_name: CPUStress
        target_kind: container
        seed_tier: degraded
        description: bad feature
        entry_signature:
          entry_window_sec: 30
          required_features:
            - kind: container
              feature: definitely_not_a_feature
              band: [3.0, .inf]
              magnitude_source: theoretical
          optional_features: []
          optional_min_match: 0
        derivation_layers:
          - layer: 1
            edge_kinds: [includes]
            edge_directions: [forward]
            expected_features:
              - kind: span
                feature: latency_p99_ratio
                band: [1.5, .inf]
            max_fanout: 32
        hand_offs: []
        terminals: []
        """
    p = _write(tmp_path, yaml)
    with pytest.raises(ManifestLoadError) as exc:
        load_manifest(p)
    # Pydantic Enum validation surfaces a helpful "Input should be ..." error.
    assert "feature" in exc.value.detail.lower()


# ---------------------------------------------------------------------------
# 5. Validation rule 5: hand-off targets resolve at registry load time.
# ---------------------------------------------------------------------------


def test_reject_unknown_handoff_target(tmp_path: Path) -> None:
    yaml = """\
        fault_type_name: CPUStress
        target_kind: container
        seed_tier: degraded
        description: dangling handoff
        entry_signature:
          entry_window_sec: 30
          required_features:
            - kind: container
              feature: cpu_throttle_ratio
              band: [3.0, .inf]
          optional_features: []
          optional_min_match: 0
        derivation_layers:
          - layer: 1
            edge_kinds: [includes]
            edge_directions: [forward]
            expected_features:
              - kind: span
                feature: latency_p99_ratio
                band: [1.5, .inf]
            max_fanout: 32
        hand_offs:
          - to: NoSuchManifest
            trigger:
              kind: span
              feature: error_rate
              threshold: 0.5
            on_layer: 1
            rationale: dangling
        terminals: []
        """
    p = _write(tmp_path, yaml, name="cpu_stress.yaml")
    # Per-file load passes because the fault_type_name "NoSuchManifest" is not
    # in FAULT_TYPE_TO_SEED_TIER — pick a valid one for the target check.
    yaml = yaml.replace("NoSuchManifest", "PodKill")
    p.write_text(dedent(yaml))
    # The hand-off target ``PodKill`` is a valid fault type name but no
    # PodKill manifest is loaded, so cross_validate must fail.
    with pytest.raises(ManifestLoadError) as exc:
        ManifestRegistry.from_directory(tmp_path, strict=True)
    assert "hand_off" in exc.value.detail or "handoff" in exc.value.detail.lower()


# ---------------------------------------------------------------------------
# 6. Validation rule 6: band[0] <= band[1].
# ---------------------------------------------------------------------------


def test_band_validation(tmp_path: Path) -> None:
    yaml = """\
        fault_type_name: CPUStress
        target_kind: container
        seed_tier: degraded
        description: bad band
        entry_signature:
          entry_window_sec: 30
          required_features:
            - kind: container
              feature: cpu_throttle_ratio
              band: [10.0, 3.0]
              magnitude_source: theoretical
          optional_features: []
          optional_min_match: 0
        derivation_layers:
          - layer: 1
            edge_kinds: [includes]
            edge_directions: [forward]
            expected_features:
              - kind: span
                feature: latency_p99_ratio
                band: [1.5, .inf]
            max_fanout: 32
        hand_offs: []
        terminals: []
        """
    p = _write(tmp_path, yaml)
    with pytest.raises(ManifestLoadError) as exc:
        load_manifest(p)
    assert "band" in exc.value.detail.lower()


# ---------------------------------------------------------------------------
# 7. Registry load_directory: 2 valid + 1 invalid file.
# ---------------------------------------------------------------------------


def test_registry_load_directory(tmp_path: Path) -> None:
    # Valid 1: CPUStress (degraded).
    (tmp_path / "cpu_stress.yaml").write_text(dedent(_minimal_yaml()))
    # Valid 2: MemoryStress (also degraded, container).
    mem = _minimal_yaml(fault_type_name="MemoryStress")
    (tmp_path / "memory_stress.yaml").write_text(dedent(mem))
    # Invalid: bad seed tier.
    (tmp_path / "bad.yaml").write_text(
        dedent(_minimal_yaml(seed_tier="slow"))
    )

    # Strict mode: the bad file aborts loading.
    with pytest.raises(ManifestLoadError):
        ManifestRegistry.from_directory(tmp_path, strict=True)

    # Non-strict mode: bad file skipped, the two valid manifests load.
    registry = ManifestRegistry.from_directory(tmp_path, strict=False)
    assert sorted(registry.names()) == ["CPUStress", "MemoryStress"]
    assert "CPUStress" in registry
    assert registry.get("CPUStress") is not None
    assert registry.get("MemoryStress") is not None


# ---------------------------------------------------------------------------
# 8. Registry get(): missing returns None (the documented fallback signal).
# ---------------------------------------------------------------------------


def test_registry_get_missing_returns_none() -> None:
    registry = ManifestRegistry({})
    assert registry.get("CPUStress") is None
    assert registry.get("DefinitelyMissing") is None
    assert len(registry) == 0


# ---------------------------------------------------------------------------
# Bonus sanity: the unused ``_load_yaml`` import keeps mypy honest about the
# module surface; remove if it ever drifts.
# ---------------------------------------------------------------------------


def test_loader_yaml_helper_smoke(tmp_path: Path) -> None:
    p = tmp_path / "x.yaml"
    p.write_text("a: 1\n")
    assert _load_yaml(p) == {"a": 1}
