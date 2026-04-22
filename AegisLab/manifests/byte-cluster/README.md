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
helm upgrade --install otel-demo0 oci://pair-cn-shanghai.cr.volces.com/opspai/otel-demo-aegis   --version 0.1.2   --namespace otel-demo0   --create-namespace   -f AegisLab/manifests/byte-cluster/initial-data/otel-demo.yaml   --wait --timeout 15m
```

## 7. Smoke / regression validation

Build `aegisctl` and validate the environment:

```bash
cd AegisLab
just build-aegisctl output=./bin/aegisctl
./bin/aegisctl status -o json
./bin/aegisctl cluster preflight
./bin/aegisctl regression run otel-demo-guided --output json
cd ..
```

Cluster-side checks:

```bash
kubectl get networkchaos -A
kubectl get hpa -n monitoring
kubectl -n monitoring exec deploy/clickstack-clickhouse -- clickhouse-client --query "SHOW TABLES FROM otel LIKE 'otel_%'"
```

## Notes

- This pack disables the chart-managed Alloy/Loki/Prometheus/Grafana stack and relies on ClickStack + OTel Collector instead.
- The OTel deployment collector HPA is intentionally more aggressive than the collector `memory_limiter`; if the limiter still fires, increase `maxReplicas` or lower the HPA targets further.
- The rcabench init containers now seed etcd through the etcd HTTP API using the mirrored `busybox` image, so they no longer depend on a separate `etcdctl` image pull.
- Most runtime images now point directly at `pair-diag-cn-guangzhou.cr.volces.com/pair/*` or `pair-cn-shanghai.cr.volces.com/opspai/*`.
