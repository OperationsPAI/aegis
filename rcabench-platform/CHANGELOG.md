# CHANGELOG

## Unreleased

### Added — FORGE rework Phase 1 (manifest plumbing)

- New `manifests/` package under `src/rcabench_platform/v3/internal/reasoning/`:
  - `schema.py` — Pydantic v2 `FaultManifest`, `EntrySignature`,
    `DerivationLayer`, `FeatureMatch`, `HandOff`, `HandOffTrigger`. Implements
    SCHEMA.md validation rules 1, 2, 4, 6, 7, 8 at model-construction time.
  - `loader.py` — `load_manifest(path)` reads YAML and returns a validated
    manifest, raising `ManifestLoadError` with file path + actionable detail.
  - `registry.py` — `ManifestRegistry.from_directory(path)` loads all
    `*.yaml`; `cross_validate()` covers rules 3 (target_kind cross-check
    with `injection.py`) and 5 (hand-off targets resolve). Process-wide
    singleton accessors `get_default_registry()` / `set_default_registry()`.
  - `features.py` — bootstrap 14-feature canonical vocabulary as `Feature`
    Enum with per-feature kind / value-type / extraction-adapter metadata.
  - `context.py` — `ReasoningContext` dataclass: per-case carrier exposing
    the active `FaultManifest | None`. Phase 3 wires this into the new
    manifest-aware gates; Phase 1 only constructs it for the contract.
  - `lint.py` — CLI: `python -m ...manifests.lint <dir>` runs all 8
    validation rules, exits 0 / 1 / 2.
- `algorithms/gates/manifest_entry.py` and `manifest_layer.py` — Phase 1
  stubs (`ManifestEntryGate`, `ManifestLayerGate`). Constructible but
  `evaluate()` raises `NotImplementedError`. Intentionally **not** added
  to `default_gates()` so production paths cannot trip the stubs.
- `cli.py` — `--manifest-dir` flag on `run` and `batch` subcommands.
  Defaults to package-shipped `manifests/fault_types/`. Empty / missing
  dir produces an empty registry, preserving the generic-rule fallback
  on every fault type. `run_single_case` logs
  `"no manifest for %s, using generic rules"` when a fault type has no
  registered manifest.
- `docs/forge_rework/examples/cpu_stress.yaml` — worked CPUStress example
  for the lint CLI smoke test (hand-off omitted so the example
  cross-validates standalone — the hand-off is exercised in the test
  fixture).
- `ir_tests/test_manifests.py` — 9 tests covering: round-trip load,
  validation rules 1/2/4/5/6, registry directory load with mixed valid /
  invalid files, missing-name → `None` fallback semantics, plus a YAML
  helper smoke test.
- `ir_tests/fixtures/cpu_stress.yaml` — full SCHEMA.md worked example
  including a `HTTPResponseAbort` hand-off (used only by single-file
  load tests; hand-off targets are not cross-validated when loading
  files directly through `load_manifest`).

### Notes

- **Task 1.1 revert is a no-op on this branch.** The "4 tightening
  fixes" (drift / topology picked-state, policy epsilon 5→2/10→5,
  temporal `-eps` → `-1`) were never committed to `origin/main`; they
  exist only as uncommitted edits in the orchestrator's working tree.
  This branch is a fresh worktree from `origin/main`, so it already
  matches the pre-tightening state for these four files. No code change
  was required.
- **`ReasoningContext` is a new dataclass rather than a retrofit of an
  existing context type.** The IR pipeline currently has no end-to-end
  reasoning-level context object (`AdapterContext` is scoped to the IR
  builder phase). Phase 3 will plumb `ReasoningContext` into the new
  gates rather than overload an existing struct.
