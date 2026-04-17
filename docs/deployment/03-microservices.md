# 03. Benchmark Microservices

This step was not executed because the cluster was never created, but the repo currently points at OpenTelemetry Demo rather than Train Ticket for the cluster bootstrap path.

## Workload selected by existing config

Observed in [AegisLab/scripts/start.sh](/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-3/AegisLab/scripts/start.sh):

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
