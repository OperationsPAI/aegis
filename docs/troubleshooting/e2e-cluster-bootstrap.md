# Fresh-Cluster E2E Bootstrap Runbook

Stand up a kind cluster and drive `aegisctl inject guided` through to a
populated datapack. Derived from the 2026-04-18 debugging session. This is a
debugging runbook, not the primary contract reference; use
`AegisLab/docs/aegisctl-cli-spec.md` for the supported validation flow, then
come back here when the environment needs repair. Work through sections in
order; each step that is a workaround (not a permanent fix) is tagged
**[WORKAROUND]**.

Short-form pitfalls memory:
`~/.claude/projects/-home-ddq-AoyangSpace-aegis/memory/aegislab_e2e_pitfalls.md`.

---

## 1. Prerequisites

- kind cluster up (single node is fine for dev)
- Chaos Mesh installed in the cluster
- MySQL, Redis, etcd deployed via the platform helm charts
- OpenTelemetry Collector in the `otel` namespace, writing to ClickHouse
  (`clickhouse.otel.svc.cluster.local:8123`, DB `otel`, user `default`,
  password `clickhouse`)
- ClickHouse receiving all three signal types (traces, metrics, logs) with
  `k8sattributes` processor enabled (pod + namespace RBAC)
- Local helm chart cache populated:
  ```bash
  ls ~/.cache/helm/repository/opentelemetry-demo-*.tgz
  # should list e.g. opentelemetry-demo-0.40.7.tgz
  ```

---

## 2. One-Time Registrations (survive across inject runs)

### 2.1 Register `otel-demo` in the `systems` table

Dev seed omits it. **[WORKAROUND — should ship in seed]**

```sql
INSERT INTO systems
  (name, display_name, ns_pattern, full_pattern, is_active, is_public, is_default,
   created_at, updated_at)
VALUES
  ('otel-demo', 'OpenTelemetry Demo', '^otel-demo\\d+$', '^(otel-demo)(\\d+)$',
   1, 1, 1, NOW(3), NOW(3));
```

### 2.2 Point `helm_configs` at a pre-downloaded chart

Remote chart repo over HTTPS fails with `x509: certificate signed by unknown
authority`. Pre-populate and switch to local. **[WORKAROUND — fix cluster
trust store or mirror the repo]**

```sql
UPDATE helm_configs
SET    local_path = '/tmp/opentelemetry-demo.tgz'
WHERE  id = 2;
```

### 2.3 Patch backend ConfigMap — ClickHouse + service FQDN

`aegislab-backend-rcabench-config` key `config.prod.toml` must contain:

```toml
[database.clickhouse]
host     = "clickhouse.otel.svc.cluster.local"
port     = "8123"
db       = "otel"          # ← `db`, NOT `database`. database/database.go:36 reads database.clickhouse.db.
                           # Writing `database = "otel"` silently produces DB_DATABASE="" in datapack pods
                           # → "Unknown table expression identifier 'otel_metrics_gauge'".
user     = "default"
password = "clickhouse"
timezone = "UTC"           # ClickHouse stores Timestamp in UTC. Without this the datapack window
                           # shifts by the pod's local TZ (Asia/Shanghai → 8h off → zero rows → validation fails).

[k8s.service]
# FQDN required: backend in default ns, datapack job in exp ns.
# Short-name DNS does not resolve cross-ns.
internal_url = "http://aegislab-backend-exp.default.svc.cluster.local:8080"
```

Apply:
```bash
kubectl -n default edit configmap aegislab-backend-rcabench-config
kubectl -n default rollout restart deploy aegislab-backend
```

### 2.4 Seed a benchmark container_version pointing at clickhouse_dataset

BuildDatapack's job image = the **benchmark** `container_version`'s
`{registry}/{namespace}/{repository}:{tag}`. It is not a separate config key.
So any `--benchmark-name X --benchmark-tag Y` you pass to `aegisctl inject guided`
must resolve to an existing row whose image actually runs the datapack.

Dev seed ships `otel-demo` as `type=2` (pedestal) only. Add a benchmark row:

```bash
kubectl exec -n default aegislab-backend-mysql-0 -- sh -c 'mysql -u root -p$MYSQL_ROOT_PASSWORD rcabench -e "
  INSERT INTO containers (name, type, readme, is_public, status, created_at, updated_at)
  VALUES (\"otel-demo-bench\", 1, \"otel-demo benchmark (datapack image)\", 1, 1, NOW(3), NOW(3));"'
  # NOTE: containers.active_name is VIRTUAL GENERATED — never include it in INSERT
  #       (MySQL errors 3105 if you try). It's derived from name.

CID=$(kubectl exec -n default aegislab-backend-mysql-0 -- sh -c 'mysql -u root -p$MYSQL_ROOT_PASSWORD rcabench -sN -e
                  "SELECT id FROM containers WHERE name=\"otel-demo-bench\";"')

kubectl exec -n default aegislab-backend-mysql-0 -- sh -c "mysql -u root -p\$MYSQL_ROOT_PASSWORD rcabench -e '
  INSERT INTO container_versions
    (name, name_major, name_minor, name_patch,
     registry, namespace, repository, tag,
     command, container_id, user_id, status, created_at, updated_at)
  VALUES
    (\"1.0.0\", 1, 0, 0,
     \"pair-diag-cn-guangzhou.cr.volces.com\", \"pair\", \"clickhouse_dataset\", \"latest\",
     \"bash /entrypoint.sh\", $CID, 1, 1, NOW(3), NOW(3));'"
```

Two traps here:
- Benchmark rows **must** be `type=1`. `CheckContainerExistsWithDifferentType`
  rejects cross-type collisions with a confusing
  *"container exists but has type 'pedestal', not 'benchmark'"*.
- If `container_versions.command IS NULL` the pod exits with
  `runc exec: "": executable file not found`. Always set a command
  (`bash /entrypoint.sh` for the clickhouse_dataset image).

### 2.4b Apply the `repository/container.go:204` SQL fix

Pre-existing bug: after the `user_containers` JOIN, the WHERE clause is
`status = ?` — ambiguous between `containers.status` and `uc.status`. Any
submit with a non-existent container name hits this before it can return
"not found", producing a 500 on `/inject`.

Fix (already in this session's uncommitted diff):

```go
// src/repository/container.go:204 (CheckContainerExistsWithDifferentType)
Where("containers.name = ? AND containers.type != ? AND containers.status = ?", ...)
```

Rebuild and reload per §4.1–4.2.

### 2.5 Mirror upstream images once per cluster

Private registry `pair-diag-cn-guangzhou.cr.volces.com/pair/open-telemetry-demo:2.2.0-*`
404s. Upstream `docker.io/otel/demo:2.2.0-*` exists. `kind load docker-image`
fails on OCI index images — use `docker save | ctr import` per node.
**[WORKAROUND — publish real mirror images]**

```bash
COMPONENTS=(accounting ad cart checkout currency email flagd-ui fraud-detection \
            frontend frontend-proxy image-provider kafka load-generator payment \
            product-catalog quote recommendation shipping react-native-app)
REGISTRY=pair-diag-cn-guangzhou.cr.volces.com/pair/open-telemetry-demo

for c in "${COMPONENTS[@]}"; do
  docker pull "docker.io/otel/demo:2.2.0-${c}"
  docker tag  "docker.io/otel/demo:2.2.0-${c}" "${REGISTRY}:2.2.0-${c}"
  docker save "${REGISTRY}:2.2.0-${c}" -o /tmp/img.tar
  docker exec kind-control-plane ctr --namespace=k8s.io images import /tmp/img.tar
done

# Plus these separately:
for ref in ghcr.io/open-feature/flagd:latest \
           docker.io/valkey/valkey:7-alpine \
           docker.io/busybox:latest \
           docker.io/opensearchproject/opensearch:2.11.1; do
  docker pull "$ref"
  docker save "$ref" -o /tmp/img.tar
  docker exec kind-control-plane ctr --namespace=k8s.io images import /tmp/img.tar
done
```

---

## 3. Per-Pedestal-Cycle Config

Run after every `restart_pedestal` task (these are wiped when the otel-demo
helm release reinstalls). **[WORKAROUND — bake into values file / backend image]**

### 3.1 Re-copy chart tgz into producer pod

```bash
PRODUCER=$(kubectl -n exp get pod -l app=aegislab-producer -o name | head -1)
kubectl -n exp cp ~/.cache/helm/repository/opentelemetry-demo-0.40.7.tgz \
  "${PRODUCER#pod/}:/tmp/opentelemetry-demo.tgz" -c exp
```

### 3.2 Fix `OTEL_COLLECTOR_NAME` at the helm values file (one-shot, persistent)

Upstream values hardcodes
`opentelemetry-kube-stack-deployment-collector.monitoring.svc.cluster.local`,
which does not exist in dev. `kubectl set env` on the deploys is **not durable** —
every `restart_pedestal` re-runs `helm upgrade` and reapplies the values file.

Fix at the source (inside the producer pod):

```bash
PRODUCER=$(kubectl -n default get pod -l app=aegislab-backend-producer -o name | head -1 | sed 's|pod/||')
VALUES_PATH=/var/lib/rcabench/dataset/helm-values/otel-demo_values_1776435696.yaml
# (path stored in helm_configs.value_file for chart id=2; may differ per install)

kubectl -n default cp "$PRODUCER:$VALUES_PATH" /tmp/vals.yaml -c exp
sed -i 's|opentelemetry-kube-stack-deployment-collector.monitoring.svc.cluster.local|otel-collector.otel.svc.cluster.local|g' /tmp/vals.yaml
kubectl -n default cp /tmp/vals.yaml "$PRODUCER:$VALUES_PATH" -c exp
```

Same pattern applies to any other env hardcoded by the upstream chart:
fix the values file, not the live deploys.

---

## 4. Driving the Test

### 4.1 Build `aegislab-backend:local` via the multi-repo Dockerfile

`src/Dockerfile.e2e` uses `replace ../../chaos-experiment` from go.mod — build
context must be the parent `aegis/` directory.

```bash
cd /home/ddq/AoyangSpace/aegis
docker build -f AegisLab/src/Dockerfile.e2e -t aegislab-backend:local .
```

### 4.2 Load into kind

For **locally-built** backend images, `kind load image-archive` works (unlike
OCI-index upstream images in §2.4 which need `ctr import`):

```bash
docker save aegislab-backend:local -o /tmp/aegislab.tar
kind load image-archive /tmp/aegislab.tar --name aegis-local
kubectl -n default rollout restart deploy aegislab-backend-producer
kubectl -n default rollout status  deploy aegislab-backend-producer
```

**Don't** use `docker cp <tarball> <kind-node>:/tmp/... && docker exec <node> ctr import`
— `docker cp` to the kind control-plane container silently drops the file
somewhere `ctr` can't see it. Either `kind load image-archive`, or pipe `docker save`
through `docker exec -i <node> ctr --namespace=k8s.io images import -`.

### 4.2b `kubectl port-forward` stability

`port-forward` dies silently on every `rollout restart` and subsequent pod
churn. The process stays alive as a zombie, so `ps` sees it but `curl
localhost:28080` returns `connection refused`. `aegisctl` calls then fail
with exit code 144 (SIGPIPE) and no error. Pattern that works reliably:

```bash
pkill -f "port-forward" 2>/dev/null; sleep 1
kubectl port-forward -n default svc/aegislab-backend-exp 28080:8080 > /tmp/pf.log 2>&1 &
disown
sleep 5
curl -sf http://localhost:28080/system/health >/dev/null || echo "pf not up yet"
```

### 4.3 Build aegisctl, auth, submit guided inject

```bash
cd /home/ddq/AoyangSpace/aegis/AegisLab
go build -o bin/aegisctl ./src/cmd/aegisctl

./bin/aegisctl auth login --token "$AEGIS_TOKEN"
./bin/aegisctl inject guided \
  --system otel-demo \
  --fault-type pod-failure \
  --target cart \
  --duration 2m \
  --apply
```

### 4.3b Iteration — bypass batch dedup

Dedupe keys on the resolved `GuidedConfig` including numeric params. Re-submitting
the same session during debugging returns
`{items: [], warnings: {batches_exist_in_database: [0]}}`. Bump any numeric
field (e.g. `--latency 500` → `--latency 600`) between retries.

### 4.3c "datapack.build.succeed → datapack.build.failed" in the same second

This is **not** a datapack failure. The CRD callback chain immediately submits
RunAlgorithm, which fails with *"algorithm container '' not found: no algorithm
containers available in database for user 1"* whenever the project has no
algorithm container configured. The trace status flips to Failed, but the
12 parquets + env/injection/k8s.json are already on the JuiceFS PVC. Check
`k8s_handler.go:62` in producer logs to distinguish before assuming datapack
itself is broken — verify the files via a busybox pod mounting
`rcabench-juicefs-dataset`.

### 4.4 Watch progression

```bash
# Task state machine
mysql -h ... -e "SELECT id, task_type, status, scheduled_at FROM tasks \
                 WHERE created_at > NOW() - INTERVAL 10 MINUTE ORDER BY id DESC;"

# Chaos CRDs (may be auto-deleted after duration+grace)
kubectl -n otel-demo0 get podchaos,networkchaos,httpchaos -o wide
```

---

## 5. Verification Checkpoints

| # | Check | Command |
|---|-------|---------|
| 1 | Backend healthy | `curl -sf http://<backend>/system/health` |
| 2 | Legacy endpoints gone | `curl -o /dev/null -w '%{http_code}' .../v2/injections/translate` → `410` |
| 3 | Live traces flowing | `SELECT COUNT(*) FROM otel.otel_traces WHERE ServiceName LIKE 'otel-demo%' AND Timestamp >= now() - INTERVAL 5 MINUTE` |
| 4 | Correct selector in use | `kubectl -n otel-demo0 get podchaos -o yaml \| grep -A2 labelSelectors` → shows `app.kubernetes.io/name: <app>` |
| 5 | Consumer finished | `SELECT id, state FROM fault_injection ORDER BY id DESC LIMIT 1;` → `state=3` |
| 6 | Datapack produced | `kubectl -n exp logs -l job-name=<datapack-job>` then check 12 parquet + 3 JSON files (see `datapack-schema.md`) |

---

## 6. Known Gaps (will bite next cluster)

See `aegislab_e2e_pitfalls.md` memory for the terse list. Items flagged
**[WORKAROUND]** above are the unfixed upstream issues; everything else is
cluster-configuration that this runbook walks you through.

Track as real aegisctl gaps (not workarounds):
- `aegisctl cluster preflight` — check-only validation for the dependency and
  readiness side of §2
- `aegisctl cluster prepare local-e2e --dry-run` — preview the Aegis-specific
  namespace/service-account/PVC/etcd work that local e2e needs
- `aegisctl cluster prepare local-e2e --apply` — apply that Aegis-specific prep
  contract without wrapping generic `kind`, `helm`, or ad-hoc `kubectl apply`
  cluster lifecycle steps
- `aegisctl pedestal helm set --chart-tgz <path>` — would eliminate §3.1
- `aegisctl container version set-image` — would eliminate §2.4 patching
