# LEGACY — not exercised by the validated cold-start flow

**Status as of 2026-05-12 (commit `17afc99`):** standalone RBAC manifest
(`rbac.yaml`). The validated cold-start runbook
([`docs/deployment/cold-start-kind.md`](../../../docs/deployment/cold-start-kind.md))
installs chaos-mesh via the upstream Helm chart
(`chaos-mesh/chaos-mesh --version 2.8.0`), which ships its own RBAC.
This file is not applied.
