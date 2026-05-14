# LEGACY — staging helm values, not validated on kind

**Status as of 2026-05-12 (commit `17afc99`):** `rcabench.yaml` +
`openebs.yaml` for a staging cluster. The validated cold-start runbook
([`docs/deployment/cold-start-kind.md`](../../../docs/deployment/cold-start-kind.md))
runs on local kind with `manifests/kind/rcabench.yaml` + the NFS
provisioner chart; nothing here is applied by the validated flow.
