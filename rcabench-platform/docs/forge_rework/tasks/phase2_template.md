# Phase 2 Manifest Authoring — Common Protocol

This file is shared protocol for all Phase 2 manifest agents (mfst-A through mfst-F). Each family has its own task file (`phase2_A_lifecycle.md`, etc.) that lists the fault types in scope.

**All agents read this file FIRST**, then their specific family file, then `SCHEMA.md`.

## Common deliverable shape

For each fault type in your family:

1. One YAML file at `src/rcabench_platform/v3/internal/reasoning/manifests/fault_types/<snake_case_name>.yaml` conforming to `SCHEMA.md`.
2. One paragraph in `docs/forge_rework/manifest_evidence/<fault_type>.md` explaining:
   - Mechanism summary (1–3 sentences)
   - How `entry_signature.required_features` were chosen (citation: chaos doc, code path, or empirical sample)
   - How magnitude bands were chosen (cite injection params + observed values)
   - Notable cascade paths and rationale for each `derivation_layer`
   - Any hand-offs added and why
3. Citation of the 3–5 sample injection cases used to calibrate empirical bands.

## How to gather empirical magnitude bands

For each fault type τ:

1. Find sample cases in dataset:
   ```bash
   ls /home/ddq/AoyangSpace/dataset/rca/ | grep -i <keyword>
   ```
   Use 3–5 cases per fault type. If <3 cases exist, mark all bands `magnitude_source: theoretical` and document the gap.

2. For each sample case, read its `injection.json` for params (duration, intensity, target).

3. Read the case's telemetry to extract observed feature values:
   - Trace data: usually under `<case_dir>/traces/` or `<case_dir>/spans/`
   - Metric data: usually under `<case_dir>/metrics/`
   - These paths vary; explore one case carefully first to understand the format
   - The platform code at `src/rcabench_platform/v3/internal/reasoning/ir/adapters/` shows exactly which fields are extracted from raw telemetry; use it as a guide.

4. Compute per-feature observed magnitude (5th, 50th, 95th percentile across the 3–5 cases) and use [5th, 95th] as the empirical band, **widened by ×0.8 / ×1.25** for safety margin.

5. For features with sparse evidence (fewer than 3 cases hit a non-zero value), keep `magnitude_source: theoretical` with a band derived from mechanism + injection param.

## Channel/edge-kind cheat sheet (TrainTicket)

| Edge kind | Direction | Source kind | Dest kind | Typical use |
|---|---|---|---|---|
| `calls` | forward | span | span | RPC caller → callee |
| `calls` | backward | span | span | RPC callee → caller (used for upstream propagation) |
| `includes` | forward | service | span | Service contains its spans |
| `routes_to` | backward | pod | service | Pod → service rollup |
| `runs` | forward | pod | container | Pod runs containers |
| `runs` | backward | container | pod | Container runs in pod |
| `schedules` | forward | node | pod | Node schedules pod |

## Magnitude calibration discipline

- **Don't pad bands "just to be safe"**. Tight bands are the whole point — wide bands let background variation through.
- Default `entry_signature.required_features` band: lower bound = max(theoretical floor, p5 empirical / 1.25). Upper: `.inf` unless mechanism caps it (e.g., `error_rate` capped at 1.0).
- Default `derivation_layers[k].expected_features` band: lower bound = lower of layer 0 × decay^k where decay ∈ [0.5, 0.8] depending on channel.
- If you can't justify a band beyond "feels reasonable", flag it in the evidence MD and use `magnitude_source: theoretical` with a citation.

## Hand-off authoring rules

Per PLAN.md risk register, hand-offs are limited:

- **Within-family hand-offs**: allowed freely (e.g., NetworkLoss → NetworkPartition when loss rate saturates).
- **Cross-family hand-offs**: ONLY via these two universal triggers:
  - `span.error_rate > 0.2` → `HTTPResponseAbort` or `JVMException`
  - `span.silent` → `NetworkPartition` or `DNSError`
- Mark every hand-off with `rationale:` of 1–2 sentences. If you can't articulate why, don't add it.
- Hand-offs MUST list `to:` as a fault_type that is being manifested by some Phase 2 agent. Coordinate via the shared hand-off table at the bottom of `PLAN.md` if extending.

## Self-validation before declaring done

Before declaring your family complete, run:

```bash
python -m rcabench_platform.v3.internal.reasoning.manifests.lint \
    src/rcabench_platform/v3/internal/reasoning/manifests/fault_types/
```

It must exit 0 across **all** manifests in the directory (including manifests authored by other families). If it fails on someone else's file, report but don't fix — surface to orchestrator.

## Acceptance criteria (per family)

- [ ] One valid YAML per fault type listed in your family file.
- [ ] One evidence MD per fault type.
- [ ] All YAMLs pass the lint CLI.
- [ ] All YAMLs pass cross-validation (hand-off targets resolve).
- [ ] Each evidence MD names the sample cases used (with case directory paths).

## Deliverables

Single branch `forge-rework-phase2-<family>` containing only:

- New YAML files under `manifests/fault_types/`
- New MD files under `docs/forge_rework/manifest_evidence/`
- No Python source changes.

## Out of scope

- Modifying schema.py, loader.py, registry.py (Phase 1 territory).
- Modifying any algorithm code (Phase 3).
- Running validation experiments (Phase 4).
