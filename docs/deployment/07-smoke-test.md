# 07. Smoke Test

This smoke test could not be executed in this pass because the local cluster never became healthy.

## Intended end-to-end path

1. Open the frontend.
2. Submit a network-delay fault against the benchmark workload.
3. Confirm a Chaos Mesh CR is created in Kubernetes.
4. Confirm traces/metrics/logs are collected.
5. Optionally export a datapack and hand it to `rcabench-platform`.

## Verification commands once the cluster exists

Frontend running locally:

```bash
cd AegisLab-frontend
VITE_API_TARGET=http://127.0.0.1:8082 pnpm dev
```

Cluster checks:

```bash
kubectl get networkchaos -A
kubectl describe networkchaos -A
kubectl get pods -n exp
kubectl logs -n exp deploy/rcabench-consumer
kubectl get pods -n monitoring
```

Expected:
- a `NetworkChaos` resource appears after submitting the fault
- target workload pods show the effect
- backend/consumer logs show task progress rather than transport errors
- observability pods remain healthy and collect data

## Current stop point

Attempted up to:
- cluster bootstrap only

Stopped because:
- no working `kind` cluster was available, so UI-driven fault injection could not be exercised
