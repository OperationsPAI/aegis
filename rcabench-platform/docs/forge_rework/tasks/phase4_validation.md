# Phase 4: Validation — Per-family FP/recall + manifest iteration

**Owner**: 1 agent (`validate`)
**Worktree**: yes
**Depends on**: Phase 3 merged
**Wall time target**: 3 days

## Goals

Run the full pipeline with manifest-driven gates on the canonical 500-case dataset and report:
- Joint FP on fault-free harness (target: ≤2.0%)
- Real-injection attributed rate, both global and per-family (target: ≥95% global, ≥90% per family)
- Where each fault type sits on the precision/recall surface
- Specific manifest bands that need adjustment (one iteration of tuning, not infinite loops)

## Tasks

### 4.1 Run fault-free harness

```bash
cd /home/ddq/AoyangSpace/aegis/rcabench-platform
PYTHONHASHSEED=0 uv run python bin/paper_artifacts/sham_injection_fp.py --mode v2 \
    --out output/forge_rework/sham_fp_postrework.json
```

Report:
- Total cases, label distribution, Joint FP rate
- Per-fault-type FP breakdown (which manifests are most permissive on sham roots)
- Compare to pre-rework baseline (8.2%) and tightened-gates baseline (3.0%)

### 4.2 Run real-injection harness

```bash
cd /home/ddq/AoyangSpace/aegis/rcabench-platform
PYTHONHASHSEED=0 uv run python bin/paper_artifacts/ablations_table.py \
    --dataset /home/ddq/AoyangSpace/dataset/rca \
    --workers 12 --configs baseline \
    --out output/forge_rework/baseline_real_postrework.json
```

Report:
- Global attributed rate (target ≥95%, current tight-gate baseline 72%)
- Per-family attributed rate (A–F)
- Per-fault-type attributed rate (which fault types fall below 90%)
- Mean path count per attributed case (sanity check; should be much lower than current 13.06 — manifest paths are bounded)
- Per-case hand-off statistics (how many paths required ≥1 hand-off; if a hand-off fires often, it's a load-bearing manifest connection)

### 4.3 Diagnose per-fault-type underperformance

For any fault type with attributed rate <90%:

- Sample 5 failing cases.
- For each, identify which manifest gate rejected and at which layer.
- Determine root cause:
  - **Entry signature too tight**: real telemetry doesn't satisfy required_features → loosen band lower bound or move to optional_features.
  - **Derivation layer too tight**: real downstream features don't match expected_features → adjust band or add alternative feature.
  - **Hand-off missing**: cascade pivots to a different fault type but no hand-off declared.
- Apply ONE tuning iteration: edit the manifest YAML, re-run that fault type's subset, verify improvement.
- Stop after one iteration per fault type. If still <90%, escalate to orchestrator with diagnosis.

### 4.4 Diagnose per-fault-type FP contribution

For any fault type whose sham FP rate >5% (i.e., ≥25/500 sham cases attributed to this fault type):

- Sample 5 sham FP cases.
- Identify what made them pass: entry too lenient, layer band too wide, hand-off too aggressive.
- Tighten the responsible field.
- Re-run sham harness once; verify FP improvement.

### 4.5 Generate validation report

Output `output/forge_rework/VALIDATION.md` with:

- Headline numbers (FP, attributed rate)
- Per-family table
- Per-fault-type table
- List of manifest tuning iterations applied (file, field, before, after)
- Open issues / unresolved manifests with diagnosis

### 4.6 Update paper-artifact LaTeX cells

If acceptance criteria met, regenerate:

- `output/sham_fp/sham_fp_postrework_cells.tex` — for paper §validation_calibration
- `output/ablations/baseline_postrework_cells.tex` — for paper §experiments

Don't modify the paper repo directly; just produce the cells file. User merges into paper.

## Acceptance criteria

- [ ] Fault-free Joint FP ≤2.0%
- [ ] Real-injection global attributed rate ≥95%
- [ ] Per-family attributed rate ≥90% for ALL six families
- [ ] VALIDATION.md report generated
- [ ] At most ONE tuning iteration applied per fault type (not infinite loops); if more needed, escalate

If acceptance criteria not met after one iteration:

- Escalate to orchestrator with full diagnosis.
- Do NOT silently regress to "best effort" numbers.

## Out of scope

- Modifying schema.py / loader.py / registry.py (Phase 1 territory).
- Modifying gate algorithm (Phase 3 territory).
- Adding new fault types (separate change).
- Touching paper repo (orchestrator's job after this phase signs off).
