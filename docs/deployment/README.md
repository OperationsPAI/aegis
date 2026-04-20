# Deployment And Smoke-Test Runbooks

Source of truth is the repo state under `AegisLab/` as of 2026-04-20.

This directory mixes two kinds of documents:

- current operator runbooks
- archived capture logs from one-off local-cluster experiments

## Current supported workflows

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
- Repo-native regression smoke:
  `cd AegisLab && just test-regression`

## Document map

- [`prerequisites.md`](./prerequisites.md) - host prerequisites and environment assumptions
- [`01-kind-cluster.md`](./01-kind-cluster.md) - kind bootstrap notes
- [`02-chaos-mesh.md`](./02-chaos-mesh.md) - Chaos Mesh install path
- [`03-microservices.md`](./03-microservices.md) - demo workload install notes
- [`04-backend.md`](./04-backend.md) - backend-in-cluster notes and overlays
- [`05-frontend.md`](./05-frontend.md) - frontend deployment notes
- [`06-observability.md`](./06-observability.md) - observability stacks present in the repo
- [`07-smoke-test.md`](./07-smoke-test.md) - archived manual smoke checklist; prefer `just test-regression` for the current repo-supported smoke path
- [`08-aegisctl-chaos.md`](./08-aegisctl-chaos.md) - CLI-driven chaos run notes
- [`09-smoke-run.md`](./09-smoke-run.md) - archived one-off latency experiment, not the canonical operator smoke path
- [`known-gaps.md`](./known-gaps.md) - environment-specific blockers and caveats

## Important distinctions

- The backend no longer exposes the older six-service split. The dedicated split-process topology is only `api-gateway` + `runtime-worker-service`.
- `just run` and `scripts/start.sh test` are not the same thing. `just run` deploys the repo's staging profile with `skaffold`; `scripts/start.sh test` bootstraps cluster dependencies and demo components.
- The repo contains both chart-managed observability config and bootstrap-script observability config. Read [`06-observability.md`](./06-observability.md) before assuming they are the same stack.
