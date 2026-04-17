# 04. AegisLab Backend

## Host-side build checks

Executed:

```bash
cd AegisLab/src
go version
go build ./...
go build -tags=duckdb_arrow ./...
```

Result:
- both host-side builds completed successfully on this machine with Go `1.26.2`

What this means:
- `duckdb_arrow` is part of the repo’s default Docker/Skaffold build path, but it was not a locally reproducible blocker for plain `go build ./...` during this pass

## Repo-owned image build defaults

Observed in [AegisLab/src/Dockerfile](/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-3/AegisLab/src/Dockerfile) and [AegisLab/skaffold.yaml](/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-3/AegisLab/skaffold.yaml):

- Dockerfile accepts `ARG GO_BUILD_TAGS`
- `skaffold` sets `GO_BUILD_TAGS: duckdb_arrow`
- test and staging profiles target private registries rather than kind-local image loading

## Local config assumptions to override

Observed in [AegisLab/src/config.dev.toml](/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-3/AegisLab/src/config.dev.toml):

- `workspace = "/home/nn/workspace/AegisLab"`
- `k8s.service.internal_url = "http://10.10.10.161:8082"`
- `k8s.init_container.busybox_image = "10.10.10.240/library/busybox:1.35"`
- `loki.address = "http://10.10.10.161:3100"`
- `jfs.dataset_path = "/mnt/jfs/rcabench_dataset"`

Those defaults are not portable to a fresh local checkout.

## Cluster deployment path

Repo commands:

```bash
cd AegisLab
ENV_MODE=test devbox run skaffold run
```

or:

```bash
cd AegisLab
helm upgrade -i rcabench ./helm \
  --namespace exp \
  --create-namespace \
  --values ./manifests/test/rcabench.yaml \
  --set-file initialDataFiles.data_yaml=data/initial_data/prod/data.yaml \
  --set-file initialDataFiles.otel_demo_yaml=data/initial_data/prod/otel-demo.yaml \
  --set-file initialDataFiles.ts_yaml=data/initial_data/prod/ts.yaml \
  --atomic \
  --timeout 10m
```

## Verification commands once the cluster exists

```bash
kubectl get pods -n exp
kubectl get svc -n exp
kubectl logs -n exp deploy/rcabench
kubectl logs -n exp deploy/rcabench-consumer
```

Expected:
- backend and consumer pods are `Running`
- service endpoints exist in namespace `exp`
- logs do not show failed connections to `10.10.10.*` addresses

## Current stop point

Attempted up to:
- host build only

Stopped because:
- cluster bootstrap failed before image build/deploy could be attempted
