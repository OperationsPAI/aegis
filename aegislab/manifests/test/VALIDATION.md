# Validation status

**As of 2026-05-12 (commit `17afc99`):**

| File | Last validated | Notes |
|---|---|---|
| `kind-config.yaml` | 2026-05-12 | Used by the validated cold-start runbook (`kind create cluster --config manifests/test/kind-config.yaml`). |
| `rcabench.yaml` | not in this round | The runbook uses `manifests/kind/rcabench.yaml` instead. This `test/` variant has drifted and is not exercised. |

The `test/` profile itself is legacy — the only file kept alive is
`kind-config.yaml` because the kind setup happens to live here.
