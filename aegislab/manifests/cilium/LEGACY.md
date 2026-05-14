# LEGACY — Cilium CNI policies, not used on kind

**Status as of 2026-05-12 (commit `17afc99`):** Cilium L7 policy +
metrics + helm values. The validated cold-start runbook
([`docs/deployment/cold-start-kind.md`](../../../docs/deployment/cold-start-kind.md))
runs on kind with the default kindnet CNI; Cilium is not installed.

Targets a cloud cluster that has not been re-validated since at least
the 2026-04 layout reorganization.
