# 02. Chaos Mesh

This step was not executed end-to-end because [01-kind-cluster.md](./01-kind-cluster.md) never produced a healthy cluster.

## Repo-owned install path

The repo’s bootstrap script is [AegisLab/scripts/start.sh](/home/ddq/AoyangSpace/aegis/AegisLab/scripts/start.sh) and installs Chaos Mesh like this:

```bash
helm repo add chaos-mesh https://charts.chaos-mesh.org --force-update
helm install chaos-mesh chaos-mesh/chaos-mesh \
  --namespace chaos-mesh \
  --create-namespace \
  --version 2.8.0 \
  -f AegisLab/manifests/cn_mirror/chaos-mesh.yaml \
  --atomic \
  --timeout 10m

kubectl apply -f AegisLab/manifests/chaos-mesh/rbac.yaml
```

The values file used in that command is [AegisLab/manifests/cn_mirror/chaos-mesh.yaml](/home/ddq/AoyangSpace/aegis/AegisLab/manifests/cn_mirror/chaos-mesh.yaml), which rewrites Chaos Mesh images to `pair-diag-cn-guangzhou.cr.volces.com/pair/...`. A fresh local setup therefore needs registry access in addition to a working cluster.

## Version coupling to verify

`chaos-experiment/go.mod` pins the CRD API through an internal fork replacement:

```text
replace github.com/chaos-mesh/chaos-mesh/api => github.com/OperationsPAI/chaos-mesh/api v0.0.0-20260124102507-517f3df45e54
```

That means the library is not using upstream Chaos Mesh APIs directly. Before changing Chaos Mesh chart versions, re-verify:

```bash
cd chaos-experiment
go test ./...
```

## Verification commands once the cluster exists

```bash
kubectl get ns chaos-mesh
kubectl get pods -n chaos-mesh
kubectl api-resources | grep chaos-mesh
kubectl auth can-i create networkchaos.chaos-mesh.org --namespace exp
```

Expected:
- controller-manager and daemon pods are `Running`
- `networkchaos.chaos-mesh.org` and related CRDs are listed
- the service account used by Aegis has permission to create chaos resources

## Current stop point

Attempted up to:
- waiting for a working cluster from [01-kind-cluster.md](./01-kind-cluster.md)

Stopped because:
- no local Kubernetes API server was available
