# LEGACY — not exercised by the validated cold-start flow

**Status as of 2026-05-12 (commit `17afc99`):** vendored third-party
chart (`clickstack-chart/`). The validated cold-start runbook
([`docs/deployment/cold-start-kind.md`](../../docs/deployment/cold-start-kind.md))
installs the OpenTelemetry kube-stack from
`open-telemetry/opentelemetry-kube-stack` upstream and ClickHouse from
`docs/deployment/otel-pipeline.yaml`. Nothing in this directory is
loaded.

Treat as legacy until re-validated.
