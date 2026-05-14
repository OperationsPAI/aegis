# LEGACY — not exercised by the validated cold-start flow

**Status as of 2026-05-12 (commit `17afc99`):** ad-hoc scripts accreted
over time (`clean-data.sh`, `clean-k8s-data.sh`, `command/`,
`generate_http_modules.py`, `hack/`, `publish.sh`,
`reconcile-ns-count.sh`, `start.sh`, `test-push.sh`,
`test-regression.sh`). None were touched by the validated cold-start
flow ([`docs/deployment/cold-start-kind.md`](../../docs/deployment/cold-start-kind.md));
their target environments (cluster names, registries, namespaces) may be
stale.

Read each script and re-test before using. The supported CLI surface is
`aegisctl` (built from `src/cli`).
