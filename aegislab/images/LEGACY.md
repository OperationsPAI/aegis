# LEGACY — not exercised by the validated cold-start flow

**Status as of 2026-05-12 (commit `17afc99`):** auxiliary container build
context (`init-helm-charts/`). The validated cold-start runbook
([`docs/deployment/cold-start-kind.md`](../../docs/deployment/cold-start-kind.md))
pulls all images from public Docker Hub (`opspai/rcabench:latest` etc.) and
does not build or push anything from this directory.

Treat as legacy until re-tested.
