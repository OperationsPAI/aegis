# LEGACY — historical "dev" cluster setup, not used by kind

**Status as of 2026-05-12 (commit `17afc99`):** `exp-dev-setup.yaml`,
`grafana/`, `loki-config.yaml`, `prometheus.yaml`. These targeted an
older shared dev cluster. The validated cold-start runbook
([`docs/deployment/cold-start-kind.md`](../../../docs/deployment/cold-start-kind.md))
relies on the OpenTelemetry kube-stack chart for metrics+logs and does
not deploy a separate Grafana / Loki / Prometheus stack.

Not validated since the 2026-04 repo layout reorganization.
