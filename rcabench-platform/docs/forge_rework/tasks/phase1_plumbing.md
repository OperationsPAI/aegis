# Phase 1: Plumbing — Manifest runtime + gate stubs

**Owner**: 1 agent (`plumbing`)
**Worktree**: yes
**Depends on**: nothing (independent)
**Wall time target**: 3 days
**Reads**: `docs/forge_rework/PLAN.md`, `docs/forge_rework/SCHEMA.md`

## Goals

Implement the runtime that loads, validates, and consumes fault manifests; stub out new gate interfaces so Phase 3 can drop in implementations cleanly. Existing 4 tightening fixes are reverted. Generic 18-rule fallback preserved.

## Tasks

### 1.1 Revert 4 tightening fixes

Reset these files to their state on the commit immediately before the tightening series:

- `src/rcabench_platform/v3/internal/reasoning/algorithms/gates/drift.py`
- `src/rcabench_platform/v3/internal/reasoning/algorithms/gates/topology.py`
- `src/rcabench_platform/v3/internal/reasoning/algorithms/gates/temporal.py`
- `src/rcabench_platform/v3/internal/reasoning/algorithms/policy.py`

Use `git log` on each file to identify the pre-tightening commit. The 4 changes were applied in a single agent run; revert each cleanly.

### 1.2 Implement FaultManifest schema

Create `src/rcabench_platform/v3/internal/reasoning/manifests/`:

- `__init__.py` — public exports
- `schema.py` — Pydantic models matching `SCHEMA.md`. Fields:
  - `FaultManifest`: top-level, with all sections from schema.
  - `EntrySignature`: `entry_window_sec`, `required_features`, `optional_features`, `optional_min_match`.
  - `DerivationLayer`: `layer`, `edge_kinds`, `edge_directions`, `expected_features`, `max_fanout`.
  - `FeatureMatch`: `kind`, `feature`, `band: tuple[float, float]`, `magnitude_source`, `magnitude_decay`.
  - `HandOff`: `to`, `trigger`, `on_layer`, `rationale`.
  - All Pydantic v2 with proper validators.
- `loader.py` — `load_manifest(path: Path) -> FaultManifest` with YAML parsing (PyYAML), type coercion, and the 8 validation rules from SCHEMA.md §"Validation rules".
- `registry.py` — `ManifestRegistry`:
  - `from_directory(path: Path) -> ManifestRegistry`: load all `*.yaml` under `manifests/fault_types/`.
  - `get(fault_type_name: str) -> FaultManifest | None`: look up; `None` means "fall back to generic rules".
  - `cross_validate()`: verify hand-offs reference valid manifests; raise on first failure.
- `features.py` — declares the canonical feature vocabulary as an Enum + per-feature metadata (kind support, value type, extraction adapter). Bootstrap with the 14 features from SCHEMA.md.

### 1.3 Hook manifests into IR pipeline (interface only)

Don't yet rewire PathBuilder or rule_matcher to consume manifests; that's Phase 3. Just expose:

- `ReasoningContext.manifest: FaultManifest | None` populated by the IR runner from `do(v_root, fault_type_name)`.
- `ManifestRegistry` instantiated once per process (singleton in registry.py).
- `cli.py` accepts `--manifest-dir` flag (default: `manifests/fault_types/`); reads registry at startup.
- If `registry.get(fault_type_name)` returns None, log INFO `"no manifest for %s, using generic rules"` and proceed with current pipeline. **Generic rule path stays the default** when manifest is missing.

### 1.4 Stub Phase 3 gate interfaces

Add these new files (empty implementations, raise NotImplementedError):

- `algorithms/gates/manifest_entry.py` — `ManifestEntryGate`: checks v_root features against `manifest.entry_signature` within `entry_window_sec`. Phase 3 implements.
- `algorithms/gates/manifest_layer.py` — `ManifestLayerGate`: per-edge check that downstream node features match the corresponding `derivation_layers[k].expected_features`. Phase 3 implements.

These are referenced in `gates/__init__.py` but not invoked anywhere yet. PathBuilder/RuleMatcher continue using current generic gates.

### 1.5 Lint CLI

Add `python -m rcabench_platform.v3.internal.reasoning.manifests.lint <dir>` that:

- Loads every `*.yaml` in dir
- Runs all 8 validation rules
- Reports first error per file with line number
- Exits 0 if all pass, 1 otherwise

Used by Phase 2 agents and CI.

### 1.6 Unit tests

In `src/rcabench_platform/v3/internal/reasoning/ir_tests/test_manifests.py`:

- `test_load_valid_manifest`: round-trip a known-good YAML (use the worked CPUStress example from SCHEMA.md, save to `tests/fixtures/cpu_stress.yaml`).
- `test_reject_unknown_fault_type_name`: validation rule 1.
- `test_reject_seed_tier_mismatch`: validation rule 2.
- `test_reject_unknown_feature`: validation rule 4.
- `test_reject_unknown_handoff_target`: validation rule 5 (registry-level cross-validation).
- `test_band_validation`: rule 6.
- `test_registry_load_directory`: load directory with 2 valid + 1 invalid file, verify behavior.
- `test_registry_get_missing_returns_none`: fallback semantics.

All tests must pass via `pytest src/rcabench_platform/v3/internal/reasoning/ir_tests/test_manifests.py`.

## Acceptance criteria

- [ ] All 4 tightening fixes are reverted (verify via `git diff` against commit before tightening series).
- [ ] `python -m rcabench_platform.v3.internal.reasoning.manifests.lint docs/forge_rework/examples/` passes on the worked CPUStress example (Phase 1 agent must place this example file).
- [ ] All 8 unit tests in `test_manifests.py` pass.
- [ ] Existing test suite passes: `pytest src/rcabench_platform/v3/internal/reasoning/ir_tests/ -x` — green except for tests already failing on `main` before this work.
- [ ] Running canonical baseline at full N=500 still produces ~99.8% attributed rate (since no manifests exist yet → generic fallback path).
- [ ] No production code path can fail with `NotImplementedError` from the stubbed gates (they are unwired).

## Deliverables

Single PR-style branch `forge-rework-phase1-plumbing` with:

- Code under `src/rcabench_platform/v3/internal/reasoning/manifests/`
- Stub files under `algorithms/gates/`
- Tests under `ir_tests/test_manifests.py`
- Fixture `tests/fixtures/cpu_stress.yaml`
- Brief CHANGELOG entry

## Out of scope

- Implementing `ManifestEntryGate` / `ManifestLayerGate` logic (Phase 3).
- Rewiring PathBuilder to traverse manifest derivation tree (Phase 3).
- Authoring any non-CPUStress manifests (Phase 2).
- Validation against real telemetry (Phase 4).
