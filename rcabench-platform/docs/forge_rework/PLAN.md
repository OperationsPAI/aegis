# FORGE Verification Rework: Fault-Typed Compositional Derivation

## Why

Current FORGE verification stack (4 gates over a generic 18-rule state machine) produces **8.2% Joint FP on fault-free windows** with random sham roots. Tightening parameters trades FP for recall (3.0% FP at 72% attributed rate). The Pareto isn't favorable.

**Root cause** (confirmed by reading `rules/builtin_rules.json` + `models/fault_seed.py`):

- `fault_seed.py` already maps each `fault_type` to a canonical seed tier — Phase 1 of forward verification (entry seeding) is in place.
- After seeding, propagation falls back to a **generic 18-rule state-machine** that ignores `fault_type`. CPU stress, network partition, and DB latency all expand through the same `(slow → erroring/slow)` rule once the seed is set.
- The 7-state alphabet `{healthy, slow, degraded, erroring, silent, unavailable, missing}` is many-to-one over real fault signatures: natural GC, retry storms, and traffic spikes also project into `slow/degraded/erroring`, so background variation is structurally indistinguishable from real cascades.

## What

Replace the generic state-machine with a **fault-type-conditioned compositional derivation**:

- Each fault type τ owns a **manifest** Mτ = (entry_signature, derivation_layers, hand_offs).
- Verification = "does observed telemetry match Mτ's derivation tree, layer by layer?"
- Hand-offs let one fault type's downstream effect become another fault type's entry, modeling cascading faults.

Background workload variation will rarely match an entry_signature with the right magnitude band, so fault-free FP drops to <1% **without** sacrificing recall (real cascades are designed-into the manifest by construction).

## Architecture (target)

```
                  ┌────────────────────────────────────────────────┐
                  │  Injection record: do(v_root, τ, params, t0,Δt) │
                  └────────────────────┬───────────────────────────┘
                                       │
                                       ▼
              ┌──────────────────────────────────────────────┐
              │  ManifestRegistry: load Mτ for fault_type τ  │
              └────────────────────┬─────────────────────────┘
                                   │
        ┌──────────────────────────┴──────────────────────┐
        ▼                                                 ▼
  Entry check                                  Forward derivation
  - features at v_root within entry            - layer-by-layer expansion
    window match Mτ.entry_signature            - candidate downstream nodes
  - magnitude bands satisfied                    drawn from topology
  - if not: outcome = ineffective/contaminated  - feature match against
                                                  layer's expected_features
                                                - hand-off when leaf signature
                                                  matches another Mτ' entry
                                       │
                                       ▼
                ┌──────────────────────────────────────────────┐
                │   Verified path set Π* + outcome class       │
                └──────────────────────────────────────────────┘
```

## Phases & Dependencies

| Phase | Owner | Deps | Wall time | Deliverable |
|---|---|---|---|---|
| 1. Plumbing | 1 agent (`plumbing`) | — | 3 d | Manifest loader, runtime API, gate stubs, generic fallback |
| 2. Manifest catalog | 6 agents (`mfst-A` … `mfst-F`) | SCHEMA.md only | 5–7 d | YAML manifests for ~30 fault types |
| 3. Gate rework | 1 agent (`gates`) | Phase 1 + 2 | 3 d | Magnitude-band DriftGate, typed-admission TopologyGate |
| 4. Validation | 1 agent (`validate`) | Phase 3 | 3 d | Per-family FP/recall, manifest iteration |

Phases 1 and 2 run **in parallel** — Phase 2 agents only need SCHEMA.md (this repo), not Phase 1 runtime.

## Acceptance bar (global)

The rework is accepted as a whole if and only if, on the canonical 500-case dataset:

- **Fault-free Joint FP** (sham harness, random non-GT root): **≤ 2.0%** (current 8.2% with original gates, 3.0% with tightened gates)
- **Real-injection attributed rate**: **≥ 95%** (current 99.8% with loose gates, 72% with tight gates)
- **Per-family attributed rate**: **≥ 90%** for every family A–F (no family hidden behind aggregate)
- **No regression in unit tests** for components untouched by the rework. Old `test_temporal_admission.py` failures from the 4-fix tightening will be replaced by manifest-aware tests.

If acceptance bar isn't met after one validation iteration, escalate decision to user (don't loop indefinitely).

## Risk register

1. **Manifest workload underestimated** — mitigation: Family A + B (~40% of dataset) ships first; if numbers look good, scale; if not, revisit design before doing C–F.
2. **Hand-off explosion** (30² potential pairs) — mitigation: hand-offs limited to (a) within-family handoffs, (b) cross-family only via two universal triggers (`span.error_rate > 0.2`, `span.silent`).
3. **Real telemetry doesn't match theoretical signatures** — mitigation: each manifest field carries `source: theoretical | empirical` tag so disagreements are explicit; agents combine both with OR semantics.
4. **Backward compatibility break** — mitigation: generic state-machine path retained as fallback for fault types without a manifest (Phase 1 keeps it; Phase 2 only adds manifests).

## Working tree strategy

Each agent works in its own `isolation: worktree`. Orchestrator merges sequentially:

1. Plumbing agent merges first (touches Python source).
2. Manifest agents merge in alphabetical order (each writes to its own family file under `manifests/fault_types/`, no Python conflict).
3. Gates agent rebases onto merged plumbing+manifests.
4. Validation agent works on the post-gates branch.

If any merge conflicts, orchestrator resolves; agents do not communicate with each other.

## Reference: current state of art (5/1/2026)

- 4 tightening fixes from the previous round (`drift.py` picked-state, `topology.py` picked-state, `policy.py` epsilon 5→2, `temporal.py` `-eps`→`-1`) are **on `main`**. Plumbing agent **reverts these first** so the rework has a clean baseline. Tight numbers (3.0% FP / 72% attributed) are what we beat.
- Existing manifest of fault_type → seed tier lives in `models/fault_seed.py` (FAULT_TYPE_TO_SEED_TIER, 30 entries). The new manifest extends each entry with entry_signature + derivation_layers; it does **not** replace the seed tier mapping.
- 18 generic propagation rules in `rules/builtin_rules.json`. Plumbing agent keeps these as fallback for unmanifested fault types.
