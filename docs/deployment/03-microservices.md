# 03. Benchmark Microservices

This step was not executed because the cluster was never created, but the repo currently points at OpenTelemetry Demo rather than Train Ticket for the cluster bootstrap path.

## Workload selected by existing config

Observed in [AegisLab/scripts/start.sh](/home/ddq/AoyangSpace/aegis/AegisLab/scripts/start.sh):

```bash
helm repo add opentelemetry-demo https://operationspai.github.io/opentelemetry-demo --force-update
helm install otel-demo0 opentelemetry-demo/opentelemetry-demo \
  --namespace otel-demo0 \
  --create-namespace \
  -f AegisLab/data/initial_data/prod/otel-demo.yaml \
  --atomic \
  --timeout 10m
```

No equivalent train-ticket install path was found in the parent-repo docs or bootstrap script used during this pass.

## Additional bootstrap assumptions

The test-mode bootstrap path in [AegisLab/scripts/start.sh](/home/ddq/AoyangSpace/aegis/AegisLab/scripts/start.sh) hardcodes:

```bash
HTTP_PROXY=http://crash:crash@172.18.0.1:7890
HTTPS_PROXY=http://crash:crash@172.18.0.1:7890
NO_PROXY=localhost,127.0.0.1,10.96.0.0/12,172.18.0.0/16,cluster.local,svc
```

That proxy expectation is undocumented at the workspace level and is specific to the original team environment.

The workload values file [AegisLab/data/initial_data/prod/otel-demo.yaml](/home/ddq/AoyangSpace/aegis/AegisLab/data/initial_data/prod/otel-demo.yaml) also rewrites workload images to `pair-diag-cn-guangzhou.cr.volces.com/pair/...`, so even a healthy local cluster still needs access to that registry or a replacement values file.

## Verification commands once the cluster exists

```bash
helm list -A | grep otel-demo0
kubectl get ns otel-demo0
kubectl get pods -n otel-demo0
kubectl get svc -n otel-demo0
```

Expected:
- the Helm release `otel-demo0` exists
- all workload pods are `Running`
- frontend and backend services are exposed inside the namespace

## Current stop point

Attempted up to:
- repo inspection only

Stopped because:
- cluster bootstrap failed before any workload Helm install could be attempted
