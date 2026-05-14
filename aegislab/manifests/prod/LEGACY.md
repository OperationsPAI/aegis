# LEGACY — production helm values, not validated on kind

**Status as of 2026-05-12 (commit `17afc99`):** `rcabench.yaml` +
`debug-busybox.yaml` for a production deployment. The validated
cold-start runbook
([`docs/deployment/cold-start-kind.md`](../../../docs/deployment/cold-start-kind.md))
runs on local kind with `manifests/kind/rcabench.yaml`; nothing here
is applied by the validated flow.

The values here may reference image tags / registries / DNS that have
since moved.
