# 06. Observability

Two observability flows coexist in this repo: the Helm chart wires an in-cluster Alloy/Loki/Prometheus/Grafana/Jaeger stack, while `AegisLab/scripts/start.sh` bootstraps a separate demo stack based on ClickStack + OTel Kube Stack. Do not treat them as one deployment path.

## Components referenced by the repo

Observed in [AegisLab/helm/values.yaml](/home/ddq/AoyangSpace/aegis/AegisLab/helm/values.yaml):

- Prometheus
- Grafana
- Loki
- Grafana Alloy
- optional Jaeger PVCs

Observed in [AegisLab/scripts/start.sh](/home/ddq/AoyangSpace/aegis/AegisLab/scripts/start.sh):

- `clickstack/clickstack`
- `open-telemetry/opentelemetry-kube-stack`

Observed in:
- [AegisLab/manifests/cn_mirror/click-stack.yaml](/home/ddq/AoyangSpace/aegis/AegisLab/manifests/cn_mirror/click-stack.yaml)
- [AegisLab/manifests/cn_mirror/otel-kube-stack.yaml](/home/ddq/AoyangSpace/aegis/AegisLab/manifests/cn_mirror/otel-kube-stack.yaml)

Those values files rewrite observability images to `pair-diag-cn-guangzhou.cr.volces.com/pair/...`, so the stack is not locally reproducible without private-registry access or alternative values.

## Commands to run once the cluster exists

```bash
helm repo add clickstack https://hyperdxio.github.io/helm-charts --force-update
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts --force-update
```

Then use the repo bootstrap path:

```bash
cd AegisLab
bash scripts/start.sh test
```

Or install stack pieces individually by reusing the commands in that script.

## Verification commands

```bash
kubectl get ns monitoring
kubectl get pods -n monitoring
kubectl get svc -n monitoring
kubectl logs -n exp deploy/rcabench | grep -E 'loki|otlp|prometheus'
```

Expected:
- monitoring namespace exists
- clickstack and otel-kube-stack pods are `Running`
- backend logs show successful exporters or no hard failures against observability backends

## Storage note

The chart defaults to `persistence.storageType: juicefs`, so observability PVC behavior is entangled with the same JuiceFS assumptions described in [prerequisites.md](./prerequisites.md) and [known-gaps.md](./known-gaps.md).
