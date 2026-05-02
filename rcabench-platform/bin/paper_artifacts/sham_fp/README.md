# Joint FP measurement (paper §3.4 / Appendix `sec:appendix_precision_bound`)

The paper claims a 4.40% joint false-positive rate for the 4-gate FORGE
pipeline on fault-free windows. This directory holds the frozen result
that anchors that claim and the exact command that produces it.

## Reproduce

```bash
uv run python bin/paper_artifacts/sham_injection_fp.py \
    --dataset /home/ddq/AoyangSpace/dataset/openrca2_lite_v1 \
    --workers 12 --mode v2 \
    --out bin/paper_artifacts/sham_fp/sham_fp_lite_v1.json
```

Mode `v2` splits each case's `normal_traces` in half (baseline + synthetic
abnormal) so no real fault is present; any path the pipeline admits is a
joint FP.

## Last measured

| Field | Value |
|---|---|
| dataset | `/home/ddq/AoyangSpace/dataset/openrca2_lite_v1` (637 cases) |
| mode | `v2` (split-normal) |
| n_trials_admitted | 637 |
| **joint_fp_rate** | **4.40%** (28/637) |
| label distribution | attributed=28, unexplained_impact=340, ineffective=269 |
| errors | 0 |

Run ~5–10 min on 12 workers. The sham target seed is deterministic per
case (`--seed`, default 20260428), so reruns at the same code SHA on the
same dataset reproduce the count exactly.

## Sibling artifact: ablations

A `sham_injection_fp.py --mode v1` run (sham target on real cascade traces)
measures the wrong-target rate and is documented in the same script's
`--mode` flag.
