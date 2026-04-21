# Prerequisites — what must be true before phase 2

Every item here has burned hours during past onboardings. Check them in
order; any failure blocks the rest.

## Cluster

- kind cluster `aegis-local` (or similar) with `kubectl --context
  kind-aegis-local get nodes` returning Ready across all nodes.
- Host inotify limits bumped — `fs.inotify.max_user_instances >= 1024`,
  `fs.inotify.max_user_watches >= 524288`. Below these, `kind create
  cluster` fails during `Preparing nodes`. See
  `docs/deployment/known-gaps.md` for the nsenter workaround.
- `local-path-provisioner` (or equivalent) available as the default
  StorageClass. The AegisLab backend helm chart assumes it.

## Chaos Mesh

- Installed via `helm install chaos-mesh chaos-mesh/chaos-mesh
  -n chaos-mesh --version 2.8.0`.
- **Critical**: `chaosDaemon.runtime=containerd`,
  `chaosDaemon.socketPath=/run/containerd/containerd.sock`. Default is
  `docker`, which breaks on kind v1.34+. Symptom: `error while getting
  PID: expected docker:// but got container`.
- `ip_set` kernel module loaded on the host (`lsmod | grep ip_set`).
  The `chaos-daemon` image carries the `ipset` binary, but needs the
  host kernel module.
- Smoke check: any `NetworkChaos` reaches `AllInjected: True` within
  ~5s. If it spins on `Failed to apply`, stop and re-check the daemon
  runtime config.

## Observability pipeline

ClickHouse + OTEL collector running in namespace `otel`. Full manifest:
`docs/deployment/otel-pipeline.yaml` in this repo.

- ClickHouse 24.x requires a non-empty password on `default`. The
  collector's `clickhouse` exporter config must match — setting
  `CLICKHOUSE_PASSWORD=""` silently creates no user and the collector
  crashes with `Authentication failed`.
- Collector image must be `otel/opentelemetry-collector-contrib`
  (not the base `collector`) — only contrib ships the ClickHouse
  exporter, which auto-creates `otel_traces*` tables with
  `create_schema: true`.

Check all three with this script:

```bash
kubectl --context kind-aegis-local get nodes | grep -c ' Ready'  # expect >=1
kubectl -n chaos-mesh get pods | grep -c Running                  # expect >=4
kubectl -n otel exec clickhouse-0 -- clickhouse-client --password clickhouse \
  -q 'SELECT 1'                                                   # expect 1
kubectl -n otel logs deploy/otel-collector --tail=5 | grep -c "Everything is ready"  # expect 1
```

If any of these fail, fix it before proceeding. Most time lost in past
onboardings was spent debugging the workload when the real break was
one of these four lines.

## Aegis-specific tools (optional, only if you'll use path 1 of phase 4)

- `aegisctl` built from current `AegisLab/src` (`just build-aegisctl`).
- Backend API reachable (`aegislab-backend-exp` service on port 8080).
- `aegisctl auth login` successful against a `pair_diagnosis` project or
  similar.
- The target namespace must already be registered as a pedestal /
  benchmark system on the backend. If it isn't, `aegisctl chaos`
  returns HTTP 500 with `unknown namespace`. Fall back to path 2 or 3.
