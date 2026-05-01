# Phase 3: Gate rework — Manifest-driven verification

**Owner**: 1 agent (`gates`)
**Worktree**: yes
**Depends on**: Phase 1 + Phase 2 merged
**Wall time target**: 3 days

## Goals

Replace the generic 4-gate stack with a manifest-driven pipeline. When a manifest exists for the injected fault_type, verification consults it; otherwise fall back to the existing generic rules.

## Tasks

### 3.1 Implement ManifestEntryGate (replaces InjectTimeGate's role)

In `algorithms/gates/manifest_entry.py`:

- For each candidate path, check that v_root within `[t0, t0 + entry_window_sec]` satisfies `manifest.entry_signature`:
  - All `required_features` match band
  - At least `optional_min_match` of `optional_features` match
- If entry signature fails: outcome = `ineffective` (or `contaminated` if pre-injection drift exists, distinguishable via existing baseline-drift check).
- This gate runs ONCE per injection, not per path. Failure short-circuits the whole verification.

### 3.2 Implement ManifestLayerGate (replaces TopologyGate + DriftGate)

In `algorithms/gates/manifest_layer.py`:

- For each edge `(node_i, node_{i+1})` at path layer k:
  - The destination node's picked-state-time observed features must match ≥1 entry of `derivation_layers[k].expected_features`.
  - The edge kind + direction must be in `derivation_layers[k].edge_kinds` × `edge_directions`.
- Replaces both the rule-based topology check AND the binary drift check with a single magnitude-band check.
- Falls back to generic TopologyGate + DriftGate if no manifest is registered for the fault_type.

### 3.3 Adjust TemporalGate

- Keep TemporalGate, but **remove `onset_resolution(state)` from epsilon_eff**. New per-edge tolerance is just `epsilon(edge_kind)` from `_EDGE_EPSILON_SECONDS`:
  - calls: 5s, includes: 5s, routes_to: 10s, runs: 60s, schedules: 60s
- TemporalGate's reversed-order tolerance changes: `dst_onset >= src_onset - 1` (1s clock skew, no longer scaled with eps).

### 3.4 Wire PathBuilder to traverse manifest tree

- When manifest is registered: PathBuilder uses `manifest.derivation_layers` to drive expansion.
  - Layer 1 expands from v_root using `derivation_layers[0].edge_kinds × directions`, capped by `max_fanout`.
  - Layer k+1 expands from layer-k admitted nodes.
  - At each node admission, check ManifestLayerGate; reject node if features don't match.
  - Hand-offs: when a node at layer k satisfies a `hand_offs[*].trigger`, fork a new derivation rooted at that node using the target manifest's `derivation_layers`.
- When no manifest: fall back to existing PathBuilder behavior over generic rule set.

### 3.5 Update outcome classifier

- `attributed`: ≥1 path admitted by manifest pipeline, OR (no manifest path) ≥1 path admitted by generic pipeline AND SLO impact present.
- `ineffective`: entry_signature failed (no observable effect at v_root).
- `unexplained_impact`: SLO impact present but no path satisfies manifest layers.
- `absorbed`: SLO impact absent (existing logic).
- `contaminated`: existing pre-injection drift detection.

The 5-class taxonomy is preserved; manifest path adds discrimination.

### 3.6 Hand-off chain limit

Enforce the SCHEMA.md cap: ≤2 hand-offs per path (so ≤3 fault types per path). Track visited (node, fault_type) pairs to break cycles. Log warnings if cap is hit.

### 3.7 Tests

Update `ir_tests/`:

- `test_manifest_entry_gate.py`: feed CPUStress manifest + synthetic v_root features; verify pass/fail.
- `test_manifest_layer_gate.py`: feed CPUStress manifest + synthetic downstream node; verify magnitude band matching.
- `test_handoff_chain.py`: synthetic 2-hand-off chain; verify it admits at depth 2 and rejects at depth 3.
- `test_temporal_admission.py`: update assertions to match new epsilon constants (5/10/60 with no onset-resolution add).

Old `test_temporal_admission.py` failures (5 assertions) need updating to match the new constants from §3.3, NOT to pad bands back to old values.

## Acceptance criteria

- [ ] Smoke test (any 3 cases per family): each case is correctly attributed when manifest exists; falls back to generic when manifest missing.
- [ ] Hand-off chain test: synthetic chain with 2 hand-offs admits; with 3 hand-offs rejects with logged warning.
- [ ] All tests in `ir_tests/` pass.
- [ ] No fall-through to NotImplementedError stubs.

## Out of scope

- Validating against full N=500 dataset (Phase 4).
- Tuning manifest bands (Phase 4 iteration).
- Adding new feature vocabulary.
