# Validation status

**As of 2026-05-12 (commit `17afc99`):**

| Path | Last validated | Notes |
|---|---|---|
| `prod/data.yaml` | 2026-05-12 | Seed used by the validated cold-start runbook (`--set-file initialDataFiles.data_yaml=...`). |
| `prod/otel-demo.yaml` | 2026-05-12 | Same. |
| `prod/ts.yaml` | 2026-05-12 (loaded; ts not exercised this round) | Loaded into the seed, but the ts benchmark itself was not run on 2026-05-12. |
| `staging/` | not in this round | Used only by `manifests/staging/`, which is itself legacy. |

Known drift: `prod/data.yaml` declares pedestal `otel-demo` at version
`0.1.1`, but `regression/otel-demo-guided.yaml` pins `0.1.4`. The
runbook's recommended workaround is to override via a copied YAML
(see `cold-start-kind.md` step 7b).
