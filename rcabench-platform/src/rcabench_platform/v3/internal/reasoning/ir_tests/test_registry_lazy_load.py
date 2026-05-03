"""Regression: ``get_default_registry`` lazy-loads bundled manifests.

Background
----------
Phase 5 forge-rework integration shipped 31 manifest YAMLs under
``rcabench_platform/.../manifests/fault_types/``. The CLI commands
(``forge run``, ``forge run-batch``) call ``_init_manifest_registry``
which loads these YAMLs into the process-wide registry, so
``ReasoningContext.manifest`` is populated and the manifest-aware
gates kick in.

However, batch drivers under ``bin/paper_artifacts/`` (e.g.
``ablations_table.py``, ``inject_time_sensitivity.py``,
``fallback_global_start_audit.py``) and ``scripts/e2e_validate.py``
import ``run_single_case`` directly without going through the typer
entry point, so ``_init_manifest_registry`` never runs. The default
empty registry then makes every fault type fall through to generic
rules — which the JVMMemoryStress audit (output/forge_rework/
jvm_memory_stress_audit.json) measured to mis-attribute 50/50 cases
that the manifest path resolves correctly.

This test pins the lazy-load behaviour so future refactors of
``get_default_registry`` cannot silently regress the bug.
"""

from __future__ import annotations

import pytest

from rcabench_platform.v3.internal.reasoning.manifests import (
    ManifestRegistry,
    get_default_registry,
    set_default_registry,
)
from rcabench_platform.v3.internal.reasoning.manifests import registry as _reg


@pytest.fixture(autouse=True)
def _reset_registry_singleton():
    """Force every test to start with an uninitialised singleton."""
    prev = _reg._DEFAULT_REGISTRY
    _reg._DEFAULT_REGISTRY = None
    yield
    _reg._DEFAULT_REGISTRY = prev


def test_get_default_registry_lazy_loads_bundled_manifests() -> None:
    """An uninitialised registry must auto-load the package default dir.

    The 31 fault-type YAMLs ship with the package; a fresh process that
    calls :func:`run_single_case` without going through ``forge run``
    must still pick them up.
    """
    registry = get_default_registry()
    assert isinstance(registry, ManifestRegistry)
    # JVMMemoryStress is one of the 31 bundled manifests — its absence
    # is the signature of the audit's "FORGE missed every case" bug.
    assert "JVMMemoryStress" in registry.names()
    # Sanity: a known set of frequently-cited manifests should all load.
    expected_subset = {
        "JVMMemoryStress",
        "PodKill",
        "HTTPResponseDelay",
        "NetworkPartition",
        "CPUStress",
    }
    assert expected_subset.issubset(set(registry.names()))


def test_explicit_empty_registry_overrides_lazy_load() -> None:
    """Tests that pin an empty registry must keep getting an empty one.

    The synthetic-graph CLI tests rely on the generic-rule path, which
    only fires when the registry is empty. Explicit
    :func:`set_default_registry` calls must take precedence over the
    lazy-load default.
    """
    set_default_registry(ManifestRegistry({}))
    registry = get_default_registry()
    assert len(registry) == 0
    assert registry.get("JVMMemoryStress") is None


def test_lazy_load_is_memoised() -> None:
    """Repeated calls return the same instance (singleton semantics)."""
    r1 = get_default_registry()
    r2 = get_default_registry()
    assert r1 is r2
