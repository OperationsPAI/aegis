# Deployment And Smoke-Test Runbooks

Source of truth is the repo state under `AegisLab/`. The **validated** end-to-end
cold-start path is [`cold-start-kind.md`](./cold-start-kind.md) — re-walked
2026-04-23 through to `Completed` inject→datapack→algorithm with non-empty
parquets (otel-demo `PodFailure` on `cart`).

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

- [`cold-start-kind.md`](./cold-start-kind.md) - **validated** single-path runbook from zero to a Completed inject→datapack trace on a fresh kind cluster. Start here for new setups.
- [`otel-pipeline.yaml`](./otel-pipeline.yaml) - ClickHouse StatefulSet + a minimal (unused) traces-only collector. cold-start-kind applies this for ClickHouse only, then deletes the collector and replaces it with `opentelemetry-kube-stack`.
- [`networkchaos-smoke.yaml`](./networkchaos-smoke.yaml) - sample NetworkChaos for hand-tested smoke checks.
- [`kind/`](./kind/) - older standalone collector manifests (`otel-collector-{cfg,rbac,externalname}.yaml`). **Not used by cold-start-kind**; kept for reference. The runbook uses the kube-stack path instead. See [known-gaps](#known-gaps) if you prefer the standalone path.

Older numbered docs (`01-kind-cluster.md`, `02-chaos-mesh.md`, `03-microservices.md`, `05-frontend.md`, `06-observability.md`, `prerequisites.md`) were capture logs from earlier one-off bootstraps and have been removed — all current install guidance now lives in `cold-start-kind.md` section 0 (host prereqs) + sections 1-8.

## Important distinctions

- Cluster install / repair runbooks explain how to make the environment exist.
- `AegisLab/docs/aegisctl-cli-spec.md` explains the supported CLI-first validation path once that environment exists.
- The backend no longer exposes the older six-service split. The dedicated split-process topology is only `api-gateway` + `runtime-worker-service`.
- `just run` and `scripts/start.sh test` are not the same thing. `just run` deploys the repo's staging profile with `skaffold`; `scripts/start.sh test` bootstraps cluster dependencies and demo components.
