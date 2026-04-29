# Byte Cluster deploy pack

This pack deploys the AegisLab stack onto a ByteDance/Volcengine Kubernetes cluster by reusing the repo's kind-native topology, then trimming the old prod-only observability extras.

## Components in scope

- Chaos Mesh
- ClickStack / ClickHouse
- OpenTelemetry Operator + daemon collector + deployment collector
- AegisLab backend (`api-gateway` + `runtime-worker-service`) and its stateful dependencies
- AegisLab frontend
- pair-mirror-adjusted initial data for benchmark / datapack / RCA algorithm images

## Assumptions

- the cluster runtime is `containerd`
- a storage class named `rcabench` exists and can satisfy the chart's PVCs
- `metrics-server` (or an equivalent metrics API implementation) is healthy
- `pair-diag-cn-guangzhou.cr.volces.com` is reachable from the cluster
- `pair-cn-shanghai.cr.volces.com` is reachable from the cluster for the mirrored `opspai/*` images and OCI chart repo refs

## 0. Preflight

```bash
kubectl get nodes
kubectl get sc
kubectl get --raw /apis/metrics.k8s.io/v1beta1/nodes | head
helm repo add chaos-mesh https://charts.chaos-mesh.org --force-update
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts --force-update
helm repo update
# NB: ClickStack is no longer pulled from a remote repo. We vendor the
# unreleased main-branch chart under AegisLab/vendor/clickstack-chart and
# install from the local path — see step "## 2. Install ClickStack /
# ClickHouse" below.
```

Create namespaces used by the stack:

```bash
kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace exp --dry-run=client -o yaml | kubectl apply -f -
```

## 1. Install Chaos Mesh

We use the **OperationsPAI fork** of chaos-mesh (`pair-cn-shanghai.cr.volces.com/opspai/chaos-mesh:20260425-517f3df`,
mirrored from `docker.io/opspai/*`). The fork carries patches we need —
notably the `RuntimeMutatorChaos` controller for JVM mutator-agent injection
that upstream v2.8.0 does not have. The matching helm chart is published at
`https://operationspai.github.io/chaos-mesh/`, but its `index.yaml` `urls:`
fields point to a stale `lgu-se-internal.github.io` host and 404 — fetch the
chart tarball from the working `operationspai.github.io` host directly:

```bash
mkdir -p /tmp/chaos-mesh-install && cd /tmp/chaos-mesh-install
curl -sfL https://operationspai.github.io/chaos-mesh/chaos-mesh-0.0.1-test.tgz \
  -o chaos-mesh-0.0.1-test.tgz
tar -xzf chaos-mesh-0.0.1-test.tgz
helm install chaos-mesh ./chaos-mesh \
  --namespace chaos-mesh --create-namespace \
  -f /path/to/aegis/AegisLab/manifests/byte-cluster/chaos-mesh.values.yaml \
  --wait --timeout 10m
```

Why a chart-only path (no `helm repo add`): the published `index.yaml` lies
about tarball URLs, so `helm pull` against the repo fails. Pinning the
tarball download by URL keeps the install deterministic until the index is
fixed upstream.

Verify — controller-manager pods Running with **0** restarts and **24**
chaos-mesh CRDs (the upstream chart only ships 23; the 24th is
`runtimemutatorchaos.chaos-mesh.org`, which is what unblocks the JVM
mutator-agent path). If you see only 23 CRDs after install, the wrong chart
was used:

```bash
kubectl get pods -n chaos-mesh
kubectl get crd | grep -c chaos-mesh   # expect 24
kubectl get crd runtimemutatorchaos.chaos-mesh.org
```

## 2. Install ClickStack / ClickHouse

ClickStack is now installed from the **vendored unreleased chart** at
`AegisLab/vendor/clickstack-chart` (upstream main branch, pinned commit
documented in `AegisLab/vendor/clickstack-chart/SOURCE.txt`). The vendored
chart uses the ClickHouse operator (`ClickHouseCluster` and `KeeperCluster`
CRDs) instead of a plain Deployment. This gives us:

- 2 ClickHouse replicas with `ReplicatedMergeTree` semantics under a 3-pod
  Keeper quorum — closes the HA gap tracked in #208.
- HPA on the HyperDX app — first-class chart support, no longer requires
  out-of-band manifests.
- A first-class `clickhouse.cluster.spec.settings.extraConfig` knob, so the
  `max_concurrent_queries` / `max_concurrent_select_queries` overrides
  previously injected via the sibling `clickhouse-extra-config.yaml`
  ConfigMap (PR #206) are now part of the chart values. The ConfigMap +
  `kubectl patch` workaround is removed in this PR.

### 2.1 Operator prerequisite (one-time per cluster)

The chart renders `clickhouse.com/v1alpha1` CRs and requires a ClickHouse
operator that owns those CRDs to be installed cluster-wide. On the byte
cluster the `clickstack-operators-clickhouse-operator-controller` Deployment
is already running in the `default` namespace — verify before continuing:

```bash
kubectl get deploy -n default clickstack-operators-clickhouse-operator-controller
kubectl get crd clickhouseclusters.clickhouse.com keeperclusters.clickhouse.com
```

If the operator is missing on a fresh cluster, install it via the upstream
operator chart (out of scope for this pack).

### 2.2 Migration from the legacy clickstack 1.1.1 release

> **Destructive — existing OTel data in ClickHouse will be lost.** This
> migration deletes the old plain-Deployment ClickHouse pod and its PVC.
> The user has explicitly approved this trade-off; do not re-run on a
> cluster whose OTel data must be preserved.

```bash
# Drop the old release. The 1.1.1 chart owns a Deployment named
# `clickstack-clickhouse` and a Service `clickstack-clickhouse`; helm
# uninstall removes both.
helm uninstall clickstack -n monitoring

# The 1.1.1 release ran with `global.keepPVC: false` so the PVC is normally
# garbage-collected. If the PVC still exists, delete it before installing
# the new chart so the operator allocates fresh per-replica volumes:
kubectl get pvc -n monitoring | grep clickstack-clickhouse || true
kubectl delete pvc -n monitoring -l app.kubernetes.io/name=clickstack 2>/dev/null || true

# Drop the sibling extra-config ConfigMap from the PR #206 workaround. It
# is no longer referenced by anything — the new chart's spec.settings
# carries those values now.
kubectl delete configmap -n monitoring clickstack-clickhouse-extra-config --ignore-not-found

# The new chart creates Services with different names (see step 2.4).
# Delete the leftover legacy Service if helm uninstall did not remove it.
kubectl delete svc -n monitoring clickstack-clickhouse --ignore-not-found
```

### 2.3 Install the new operator-managed release

```bash
helm upgrade --install clickstack ./AegisLab/vendor/clickstack-chart \
  --namespace monitoring --create-namespace \
  -f AegisLab/manifests/byte-cluster/clickstack.values.yaml \
  --wait --timeout 15m
```

Verify the CRs are healthy and the operator has created the pods:

```bash
kubectl get clickhousecluster -n monitoring clickstack-clickhouse
kubectl get keepercluster      -n monitoring clickstack-clickhouse-keeper
kubectl get pods -n monitoring -l app.kubernetes.io/instance=clickstack
```

Expected:
- `ClickHouseCluster/clickstack-clickhouse` shows `Status=Completed`
- `KeeperCluster/clickstack-clickhouse-keeper` shows 3 ready replicas
- 2 ClickHouse server pods + 3 Keeper pods all `Running`
- HyperDX HPA exists at minReplicas=2:
  ```bash
  kubectl get hpa -n monitoring clickstack-app
  ```

### 2.4 Service-name reference

The operator creates a headless Service per cluster, named
`<release>-clickhouse-clickhouse-headless`. With release name `clickstack`
that's `clickstack-clickhouse-clickhouse-headless`. Everything that
previously dialled `clickstack-clickhouse.monitoring.svc.cluster.local` now
dials `clickstack-clickhouse-clickhouse-headless.monitoring.svc.cluster.local`
— this PR updates:

- `AegisLab/manifests/byte-cluster/otel-kube-stack.values.yaml` — both
  collector ClickHouse exporters
- `AegisLab/manifests/byte-cluster/clickhouse-init-job.yaml` — the bootstrap
  `CREATE DATABASE otel` job
- `AegisLab/manifests/byte-cluster/rcabench.values.yaml` — the rcabench
  backend's `database.clickhouse.host` config
- `AegisLab/manifests/byte-cluster/initial-data/data.yaml` — the etcd
  dynamic-config seed `database.clickhouse.host`

If anything else is still pointing at the legacy name, search for it:

```bash
grep -rn "clickstack-clickhouse\." AegisLab/manifests AegisLab/data 2>/dev/null
```

### 2.5 Bootstrap the OTel database

```bash
kubectl apply -f AegisLab/manifests/byte-cluster/clickhouse-init-job.yaml
kubectl wait --for=condition=complete job/clickstack-init-otel-db -n monitoring --timeout=5m
```

### 2.6 Verify the concurrency overrides landed via the operator

```bash
kubectl -n monitoring exec sts/chi-clickstack-clickhouse-cluster-0 -c clickhouse -- \
  clickhouse-client --query "SELECT name, value FROM system.server_settings WHERE name IN ('max_concurrent_queries','max_concurrent_select_queries')"
```

Expected: `max_concurrent_queries\t2000` and `max_concurrent_select_queries\t1500`.
The actual StatefulSet name depends on the operator's naming scheme; if
the command above does not match, fall back to:

```bash
POD=$(kubectl -n monitoring get pod -l clickhouse.com/app=clickhouse-server -o jsonpath='{.items[0].metadata.name}')
kubectl -n monitoring exec "$POD" -c clickhouse -- \
  clickhouse-client --query "SELECT name, value FROM system.server_settings WHERE name LIKE 'max_concurrent_%'"
```

## 3. Install the trimmed OTel Kube Stack

```bash
helm upgrade --install opentelemetry-kube-stack open-telemetry/opentelemetry-kube-stack   --namespace monitoring   --create-namespace   -f AegisLab/manifests/byte-cluster/otel-kube-stack.values.yaml   --set-file collectors.daemon.scrape_configs_file=AegisLab/manifests/byte-cluster/daemon-scrape-configs.yaml   --wait --timeout 10m
```

Verify the collector footprint and HPA:

```bash
kubectl get pods -n monitoring -l app.kubernetes.io/name=opentelemetry-collector
kubectl get hpa -n monitoring
kubectl describe hpa -n monitoring opentelemetry-kube-stack-deployment-collector
```

Expected:
- one daemon collector per node (handles per-node pod prometheus scrape +
  kubelet/cAdvisor + filelog — sharded by node, no cross-replica duplication)
- one deployment collector pool of `4–12` replicas (HPA bounded by
  `autoscaler.minReplicas`/`maxReplicas`; carries OTLP push + leader-elected
  k8s_cluster / k8sobjects only — no prometheus scrape, so memory pressure
  no longer scales with cluster pod count)
- CPU and memory targets both present on the HPA

History note: the deployment pool used to run prometheus pod scrape too, and
HPA chased its memory up to 120 replicas. With each replica independently
scraping every annotated target, ClickHouse received N× duplicate inserts
and went into an OOM/restart loop. Pod scrape now lives in the daemon
collector behind a `spec.nodeName` field selector — see
`daemon-scrape-configs.yaml::kubernetes-pods` and the autoscaler comment in
`otel-kube-stack.values.yaml`. Do not move it back without a sharding
mechanism (TargetAllocator or equivalent).

## 4. Install AegisLab backend/runtime

Install the backend chart with this pack's values and seed files. The frontend is deployed separately, so this path no longer depends on the removed remote frontend subchart:

```bash
cd AegisLab
helm upgrade --install rcabench ./helm   --namespace exp   --create-namespace   -f manifests/byte-cluster/rcabench.values.yaml   --set-file initialDataFiles.data_yaml=manifests/byte-cluster/initial-data/data.yaml   --set-file initialDataFiles.otel_demo_yaml=manifests/byte-cluster/initial-data/otel-demo.yaml   --set-file initialDataFiles.ts_yaml=manifests/byte-cluster/initial-data/ts.yaml   --wait --timeout 15m
cd ..
```

Verify:

```bash
kubectl get pods -n exp
kubectl get svc -n exp rcabench-api-gateway rcabench-runtime-worker-service
kubectl exec -n exp deploy/rcabench-api-gateway -- wget -qO- http://127.0.0.1:8082/system/health
```

## 5. Install the standalone frontend

```bash
kubectl apply -f AegisLab/manifests/byte-cluster/frontend.yaml
kubectl rollout status deployment/rcabench-frontend -n exp --timeout=5m
kubectl get svc -n exp rcabench-frontend
```

The frontend is exposed as `NodePort 32180` by default.

## 6. Optional: pre-install otel-demo for smoke tests

If you want a ready namespace before driving `aegisctl inject guided`, install the benchmark workload directly. To stay aligned with the seed data that AegisLab registers, use the mirrored `opspai` OCI chart:

```bash
helm upgrade --install otel-demo0 oci://pair-cn-shanghai.cr.volces.com/opspai/otel-demo-aegis   --version 0.1.4   --namespace otel-demo0   --create-namespace   -f AegisLab/manifests/byte-cluster/initial-data/otel-demo.yaml   --wait --timeout 15m
```

Verify:

```bash
kubectl get pods -n otel-demo0
kubectl get deploy -n otel-demo0 frontend-proxy -o=jsonpath='{.spec.template.spec.containers[0].resources}'
helm status -n otel-demo0 otel-demo0
```

Expected:
- every pod in `otel-demo0` is `Running`
- `frontend-proxy` has the memory override from `AegisLab/manifests/byte-cluster/initial-data/otel-demo.yaml`
- Helm release state is `deployed`

Notes:
- this pack keeps the app images on `pair-diag-cn-guangzhou.cr.volces.com/pair/*`
- the in-namespace collector stays on `pair-cn-shanghai.cr.volces.com/opspai/otel-demo-opentelemetry-collector-contrib:0.135.0`
- `otel-demo-aegis:0.1.4` now bakes the larger component resource limits into the chart, so the Byte-cluster values file no longer carries extra per-component resource overrides

## 6.1 Optional: pre-install ts namespaces

For Byte cluster validation we used `ts0` as the fault-injection target namespace, and a separate `ts1` namespace for shared use. Install them with explicit `ts-ui-dashboard` NodePorts so they do not collide:

```bash
helm install ts0 trainticket   -n ts0   --create-namespace   --repo https://operationspai.github.io/train-ticket   --version 0.1.0   -f AegisLab/manifests/byte-cluster/initial-data/ts.yaml   --set services.tsUiDashboard.nodePort=31000   --wait

helm install ts1 trainticket   -n ts1   --create-namespace   --repo https://operationspai.github.io/train-ticket   --version 0.1.0   -f AegisLab/manifests/byte-cluster/initial-data/ts.yaml   --set services.tsUiDashboard.nodePort=31001   --wait
```

Verify:

```bash
kubectl get pods -n ts0
kubectl get pods -n ts1
kubectl get svc -n ts0 ts-ui-dashboard mysql
kubectl get svc -n ts1 ts-ui-dashboard mysql
```

Expected:
- `ts0` UI NodePort is `31000`
- `ts1` UI NodePort is `31001`

## 7. CLI control validation (Byte cluster)

Expose the API gateway and run `aegisctl` against the forwarded endpoint:

```bash
kubectl port-forward -n exp svc/rcabench-api-gateway 28084:8082

cd AegisLab
HOME=/home/nn/workspace/aegis \
AEGIS_SERVER=http://127.0.0.1:28084 \
AEGIS_PASSWORD=admin123 \
./bin/aegisctl auth login --username admin

HOME=/home/nn/workspace/aegis \
AEGIS_SERVER=http://127.0.0.1:28084 \
./bin/aegisctl system list -o json
cd ..
```

Important behavior:
- all seeded systems in this pack are `is_builtin=true`
- `aegisctl system enable <builtin-system>` is rejected by backend by design (HTTP 400, cannot change status of builtin system); this was rechecked for both `otel-demo` and `ts`
- use `aegisctl pedestal chart install ...` or `aegisctl regression run ...` for benchmark/injection validation instead of enable/disable toggling
- `aegisctl pedestal chart install ts --namespace ts4 --wait` now works on this branch because the CLI can materialize backend `value_file` / inline `values` before shelling out to Helm
- `aegisctl regression run` currently false-negatives the pod preflight on this Byte cluster even when the target namespace is healthy, so use `--skip-preflight`
- repeated submissions against the same regression spec can be deduped by the backend; change the namespace/spec or wait for cooldown before retrying

## 8. Smoke / regression validation

Build `aegisctl` and validate the environment:

```bash
cd AegisLab
just build-aegisctl output=./bin/aegisctl
./bin/aegisctl status -o json
./bin/aegisctl cluster preflight
cd ..
```

Builtin-system enable check:

```bash
cd AegisLab
HOME=/home/nn \
AEGIS_SERVER=http://127.0.0.1:28084 \
./bin/aegisctl system enable otel-demo

HOME=/home/nn \
AEGIS_SERVER=http://127.0.0.1:28084 \
./bin/aegisctl system enable ts
cd ..
```

Expected: both return HTTP 400 (`cannot change status of builtin system ...`).

If `pedestal chart install` or regression gets stuck on workload readiness, check pod state first:

```bash
kubectl get pods -n otel-demo0
kubectl get events -n otel-demo0 --sort-by=.lastTimestamp | tail -n 40
```

For the Byte cluster `otel-demo` smoke path, use the repo-tracked regression case in `AegisLab/regression/otel-demo-guided.yaml`. It now tracks chart version `0.1.4`. If the backend dedupes the canonical spec, run a temporary copy with a changed batch label and/or guided spec:

```bash
cd AegisLab
HOME=/home/nn \
AEGIS_SERVER=http://127.0.0.1:28084 \
./bin/aegisctl regression run otel-demo-guided --skip-preflight --output json

cp regression/otel-demo-guided.yaml /tmp/otel-demo-guided-byte-verify.yaml
sed -i 's/value: wrapper-0.1.4-verify/value: byte-otel-demo-verify-20260423/' /tmp/otel-demo-guided-byte-verify.yaml
sed -i 's/value: local/value: byte/' /tmp/otel-demo-guided-byte-verify.yaml
sed -i 's/app: frontend/app: cart/' /tmp/otel-demo-guided-byte-verify.yaml
sed -i '0,/pre_duration: 1/s//pre_duration: 2/' /tmp/otel-demo-guided-byte-verify.yaml
sed -i '0,/duration: 1/s//duration: 2/' /tmp/otel-demo-guided-byte-verify.yaml

HOME=/home/nn \
AEGIS_SERVER=http://127.0.0.1:28084 \
./bin/aegisctl regression run --file /tmp/otel-demo-guided-byte-verify.yaml --skip-preflight --output json
cd ..
```

Observed behavior on April 23, 2026:
- `otel-demo0` was upgraded to Helm revision `3`
- `otel-demo-aegis:0.1.4` no longer needs local resource overrides for `frontend-proxy` / `flagd-ui`
- a fresh regression run passed on trace `79c12836-f9a9-457f-88ae-7224683b7f00`
- the final event was `datapack.no_anomaly`
- after reseeding `otel-demo` to `0.1.4`, `aegisctl pedestal chart install otel-demo --namespace otel-demo13 --wait` completed successfully and every pod in `otel-demo13` reached `Running`

For the Byte cluster `ts` smoke path, run the repo-tracked regression case against `ts0`. If `ts0` was already used recently and gets deduped, make a temporary copy and change at least one guided spec field:

```bash
cd AegisLab
HOME=/home/nn \
AEGIS_SERVER=http://127.0.0.1:28084 \
./bin/aegisctl regression run ts-guided --skip-preflight --output json

cp regression/ts-guided.yaml /tmp/ts-guided-byte-verify.yaml
sed -i 's/value: ts-smoke/value: ts-byte-verify-20260423/' /tmp/ts-guided-byte-verify.yaml
sed -i 's/app: ts-user-service/app: ts-food-service/' /tmp/ts-guided-byte-verify.yaml
sed -i '0,/duration: 6/s//duration: 5/' /tmp/ts-guided-byte-verify.yaml

HOME=/home/nn \
AEGIS_SERVER=http://127.0.0.1:28084 \
./bin/aegisctl regression run --file /tmp/ts-guided-byte-verify.yaml --skip-preflight --output json
cd ..
```

Observed behavior on April 23, 2026:
- `aegisctl trace watch cfc7de69-ac38-42b6-afd6-c18a64e2d2de` now replays through the final `algorithm.result.collection`
- a fresh TS regression passed on trace `4a5e011b-8335-4f87-928b-0c50532fc2f0`
- the final event was `algorithm.result.collection`
- the observed task chain was `RestartPedestal -> FaultInjection -> BuildDatapack -> RunAlgorithm -> CollectResult -> RunAlgorithm -> CollectResult`

Cluster-side checks:

```bash
kubectl get networkchaos -A
kubectl get hpa -n monitoring
# With the operator-managed cluster, exec into one of the ClickHouse pods
# directly (the legacy `deploy/clickstack-clickhouse` is gone post-migration):
POD=$(kubectl -n monitoring get pod -l clickhouse.com/app=clickhouse-server -o jsonpath='{.items[0].metadata.name}')
kubectl -n monitoring exec "$POD" -c clickhouse -- clickhouse-client --query "SHOW TABLES FROM otel LIKE 'otel_%'"
```

- `--skip-preflight` is currently needed on this Byte cluster even when the target namespace already has matching pods
- keep `ts1` free from experiments if it is reserved for other users

## Notes

- This pack disables the chart-managed Alloy/Loki/Prometheus/Grafana stack and relies on ClickStack + OTel Collector instead.
- The OTel deployment collector HPA is intentionally more aggressive than the collector `memory_limiter`; if the limiter still fires, increase `maxReplicas` or lower the HPA targets further.
- The rcabench init containers now seed etcd through the etcd HTTP API using the mirrored `busybox` image, so they no longer depend on a separate `etcdctl` image pull.
- Most runtime images now point directly at `pair-diag-cn-guangzhou.cr.volces.com/pair/*` or `pair-cn-shanghai.cr.volces.com/opspai/*`.
