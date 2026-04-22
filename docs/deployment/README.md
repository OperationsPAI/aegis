# Deployment And Smoke-Test Runbooks

Source of truth is the repo state under `AegisLab/` as of 2026-04-20.

This directory mixes two kinds of documents:

- current operator runbooks
- archived capture logs from one-off local-cluster experiments

## Current supported workflows

Kubernetes-native install steps and Aegis-specific validation steps are separate:
bring the environment up with Docker / kind / Helm / `kubectl`, then validate
AegisLab behavior with `aegisctl` once the environment exists.

- Local infra only:
  `cd AegisLab && docker compose up -d redis mysql etcd jaeger buildkitd loki prometheus grafana`
- Local backend, collocated mode:
  `cd AegisLab/src && go run . both -conf ./config.dev.toml -port 8082`
- Current split-process backend:
  `cd AegisLab/src && go run ./cmd/api-gateway -conf ./config.dev.toml -port 8082`
  plus
  `cd AegisLab/src && go run ./cmd/runtime-worker-service -conf ./config.dev.toml`
- Containerized split-process variant:
  `cd AegisLab && docker compose -f docker-compose.yaml -f docker-compose.microservices.yaml up api-gateway runtime-worker-service`
- Staging-profile cluster deploy:
  `cd AegisLab && just run`
- CLI-first validation contract (auth -> readiness -> prepare -> submit/wait):
  `cd AegisLab && less docs/aegisctl-cli-spec.md`
- Repo-native regression smoke:
  `cd AegisLab && just test-regression`

## Document map

- [`cold-start-kind.md`](./cold-start-kind.md) - **validated** single-path runbook from zero to a Completed inject→datapack trace on a fresh kind cluster (2026-04-22). Start here for new setups.
- [`prerequisites.md`](./prerequisites.md) - host prerequisites and environment assumptions
- [`01-kind-cluster.md`](./01-kind-cluster.md) - kind bootstrap notes
- [`02-chaos-mesh.md`](./02-chaos-mesh.md) - Chaos Mesh install path
- [`03-microservices.md`](./03-microservices.md) - demo workload install notes
- [`05-frontend.md`](./05-frontend.md) - frontend deployment notes
- [`06-observability.md`](./06-observability.md) - observability stacks present in the repo

## Important distinctions

- Cluster install / repair runbooks explain how to make the environment exist.
- `AegisLab/docs/aegisctl-cli-spec.md` explains the supported CLI-first validation path once that environment exists.

- The backend no longer exposes the older six-service split. The dedicated split-process topology is only `api-gateway` + `runtime-worker-service`.
- `just run` and `scripts/start.sh test` are not the same thing. `just run` deploys the repo's staging profile with `skaffold`; `scripts/start.sh test` bootstraps cluster dependencies and demo components.
- The repo contains both chart-managed observability config and bootstrap-script observability config. Read [`06-observability.md`](./06-observability.md) before assuming they are the same stack.
