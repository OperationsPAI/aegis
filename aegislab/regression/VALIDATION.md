# Validation status

**As of 2026-05-12 (commit `17afc99`):**

| Case | Last validated | Notes |
|---|---|---|
| `otel-demo-guided.yaml` | 2026-05-12 (kind) | Passed end-to-end after copying YAML and pinning `pedestal.version: "0.1.1"` to match seed; the version `0.1.4` in the checked-in file is **drifted** from `data/initial_data/prod/data.yaml`. |
| `hotelreservation-guided.yaml` | not in this round | Last green: 2026-04-21 (see memory `project_hotelreservation_integration`). |
| `socialnetwork-guided.yaml` | not in this round | Last green: 2026-04-21. |
| `mediamicroservices-guided.yaml` | not in this round | Last green: 2026-04-21. |
| `teastore-guided.yaml` | not in this round | Last green: 2026-04-21. |
| `ob-guided.yaml` | not in this round | Status unknown. |
| `sockshop-guided.yaml` | not in this round | Needs the Coherence operator install (see `docs/deployment/cold-start-kind.md` step 7b). |

All cases require `--app-label-key app.kubernetes.io/name` on kind because
the shipped charts label pods with `app.kubernetes.io/name`, not the bare
`app` key the runner defaults to.
