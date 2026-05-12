# LEGACY — Volcengine VKE / byte-cluster only, not validated on kind

**Status as of 2026-05-12 (commit `17afc99`):** values + manifests tuned
for the Volcengine "byte-cluster" cloud environment (CN mirrors, JuiceFS
CSI, ClickStack chart). The validated cold-start runbook
([`docs/deployment/cold-start-kind.md`](../../../docs/deployment/cold-start-kind.md))
runs on local kind and uses none of these files.

The most recent recorded successful cloud run is the 2026-04-24 VKE
validation (see `~/.claude/projects/.../memory/project_cloud_vke_validation.md`).
Anything older in this directory has not been re-walked since.
