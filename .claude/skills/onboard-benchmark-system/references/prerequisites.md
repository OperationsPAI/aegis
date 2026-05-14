# Prerequisites — what must be true before phase 2

Every item here has burned hours during past onboardings. Check them in
order; any failure blocks the rest.

## Cluster

- kind cluster `aegis-local` (or similar) with `kubectl --context
  kind-aegis-local get nodes` returning Ready across all nodes.
- Host inotify limits bumped — `fs.inotify.max_user_instances >= 1024`,
  `fs.inotify.max_user_watches >= 524288`. Below these, `kind create
  cluster` fails during `Preparing nodes`.
- **`HTTP_PROXY` unset** when running `kind create cluster`. kind bakes
  the host proxy into containerd; a broken one (e.g. `crash:crash@...`)
  makes every image pull fail. Clear in the shell:
  `unset HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy`.
- Default `standard` StorageClass from `local-path-provisioner` is
  **RWO only**. The aegislab chart's `containers-data` and
  `juicefs-dataset` PVCs demand RWX. Install an in-cluster NFS
  provisioner before `helm install rcabench`:
  ```bash
  helm repo add nfs-ganesha https://kubernetes-sigs.github.io/nfs-ganesha-server-and-external-provisioner
  helm install nfs-server nfs-ganesha/nfs-server-provisioner \
    -n nfs --create-namespace \
    --set storageClass.name=nfs --set persistence.enabled=true \
    --set persistence.storageClass=standard --set persistence.size=20Gi
  ```
  The kind profile `aegislab/manifests/kind/rcabench.yaml` already
  points `storageClassNames.external=nfs`.

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
`docs/deployment/otel-pipeline.yaml` + the kind-profile collector config
at `docs/deployment/kind/otel-collector-cfg.yaml` +
`otel-collector-rbac.yaml`.

- ClickHouse 24.x requires a non-empty password on `default`. The
  collector's `clickhouse` exporter config must match — setting
  `CLICKHOUSE_PASSWORD=""` silently creates no user and the collector
  crashes with `Authentication failed`.
- Collector image must be `otel/opentelemetry-collector-contrib`
  (not the base `collector`) — only contrib ships the ClickHouse
  exporter, which auto-creates `otel_traces*` tables with
  `create_schema: true`.
- **Three pipelines required** (traces, metrics, logs). A traces-only
  config works for a smoke `NetworkChaos` but BuildDatapack queries
  `otel_metrics_gauge` / `otel_logs` / etc., so the datapack builder
  fails with `UNKNOWN_TABLE` if metrics + logs pipelines are missing.
- **`k8sattributes` processor** enabled on all three pipelines, backed
  by a ClusterRole granting pods/namespaces/replicasets/deployments/nodes
  read. Without it, resource attributes miss `k8s.namespace.name` and
  BuildDatapack's filter returns 0 rows.
- **`resource` processor: `action: upsert service.namespace =
  k8s.namespace.name`**, not `insert`. Benchmark SDKs that set
  their own `service.namespace` (otel-demo ships
  `service.namespace=opentelemetry-demo`) win over `insert` and leak
  downstream. BuildDatapack filters on `service.namespace` specifically.
- **Cross-namespace collector DNS**: benchmark charts often hardcode
  `http://otel-collector:4317` as the OTLP endpoint. Since the collector
  lives in `otel`, create a stub ExternalName Service in each benchmark
  namespace pointing to `otel-collector.otel.svc.cluster.local` (see
  `docs/deployment/kind/otel-collector-externalname.yaml`). Absent this,
  pod logs show `connect ECONNREFUSED <otel-collector cluster IP>`.

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

- `aegisctl` built from current `aegislab/src` (`just build-aegisctl`).
- Backend API reachable (`aegislab-backend-exp` service on port 8080).
- `aegisctl auth login` successful against a `pair_diagnosis` project or
  similar.
- The target namespace must already be registered as a pedestal /
  benchmark system on the backend. If it isn't (`aegisctl` submit returns
  HTTP 500 with `unknown namespace` or `system ... does not match any
  registered namespace pattern`), that's a control-plane task: run
  `aegisctl system register --from-seed <seed.yaml>` followed by
  `aegisctl pedestal chart push` / `install`. Full methodology lives in
  the sibling skill `register-aegis-system` (`references/etcd.md`,
  `references/db.md`, `references/chart.md`). Don't re-derive it here.
