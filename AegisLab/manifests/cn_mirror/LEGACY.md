# LEGACY — CN-mirror values, not exercised by the validated cold-start flow

**Status as of 2026-05-12 (commit `17afc99`):** Helm values that swap
upstream images for `pair-cn-shanghai.cr.volces.com/opspai/*` mirrors
(chaos-mesh, clickstack, juicefs-csi-driver, otel-kube-stack). The
validated cold-start runbook
([`docs/deployment/cold-start-kind.md`](../../../docs/deployment/cold-start-kind.md))
pulls directly from Docker Hub and does not apply these overrides.

Use only when running from inside CN with a slow Docker Hub link, and
re-check each `image.repository` against current upstream defaults
before applying — the chart versions these were authored for may have
moved.
