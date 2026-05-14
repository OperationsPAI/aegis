# Validation status

**As of 2026-05-12 (commit `17afc99`):**

| File | Last validated | Notes |
|---|---|---|
| `otel-kube-stack.kind.yaml` | 2026-05-12 | Helm values for kube-stack on kind. Validated by the cold-start runbook. |
| `daemon-scrape-configs.kind.yaml` | 2026-05-12 (indirectly) | Bundled-into-chart fallback is what actually applies; the override path documented in older READMEs does **not** work (chart uses `.Files.Get`). |
| `otel-collector-compat-svc.yaml` | 2026-05-12 | Compat Service for benchmarks that hardcode `otel-collector:4317`. |
| `README.kind.md` | 2026-05-12 (kind portion) | Step-by-step for kind setup; superseded in part by `docs/deployment/cold-start-kind.md`. |
| `otel-kube-stack.yaml` | not in this round | Non-kind (cloud) values; last touch context unknown. |
| `daemon-scrape-configs.yaml` | not in this round | Non-kind variant; same caveat. |
