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
helm repo add clickstack https://hyperdxio.github.io/helm-charts --force-update
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts --force-update
helm repo update
```

Create namespaces used by the stack:

```bash
kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace exp --dry-run=client -o yaml | kubectl apply -f -
```

## 1. Install Chaos Mesh

```bash
helm upgrade --install chaos-mesh chaos-mesh/chaos-mesh   --namespace chaos-mesh   --create-namespace   --version 2.8.0   -f AegisLab/manifests/byte-cluster/chaos-mesh.values.yaml   --wait --timeout 10m
```

Verify:

```bash
kubectl get pods -n chaos-mesh
kubectl get crd | grep chaos-mesh
```

## 2. Install ClickStack / ClickHouse

```bash
helm upgrade --install clickstack clickstack/clickstack   --namespace monitoring   --create-namespace   -f AegisLab/manifests/byte-cluster/clickstack.values.yaml   --wait --timeout 10m
```

Verify:

```bash
kubectl get pods -n monitoring
kubectl get svc -n monitoring clickstack-clickhouse
kubectl apply -f AegisLab/manifests/byte-cluster/clickhouse-init-job.yaml
kubectl wait --for=condition=complete job/clickstack-init-otel-db -n monitoring --timeout=5m
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
- one daemon collector per node
- one deployment collector pool with at least `6` replicas
- CPU and memory targets both present on the HPA

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
kubectl -n monitoring exec deploy/clickstack-clickhouse -- clickhouse-client --query "SHOW TABLES FROM otel LIKE 'otel_%'"
```

- `--skip-preflight` is currently needed on this Byte cluster even when the target namespace already has matching pods
- keep `ts1` free from experiments if it is reserved for other users

## Notes

- This pack disables the chart-managed Alloy/Loki/Prometheus/Grafana stack and relies on ClickStack + OTel Collector instead.
- The OTel deployment collector HPA is intentionally more aggressive than the collector `memory_limiter`; if the limiter still fires, increase `maxReplicas` or lower the HPA targets further.
- The rcabench init containers now seed etcd through the etcd HTTP API using the mirrored `busybox` image, so they no longer depend on a separate `etcdctl` image pull.
- Most runtime images now point directly at `pair-diag-cn-guangzhou.cr.volces.com/pair/*` or `pair-cn-shanghai.cr.volces.com/opspai/*`.
