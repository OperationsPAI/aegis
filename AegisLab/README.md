# AegisLab (RCABench)

AegisLab is the backend and SDK workspace for RCABench, a root-cause-analysis benchmarking platform for microservice systems. It manages chaos systems, projects, datasets, injections, executions, evaluations, and the async runtime that drives build / inject / collect workflows.

## Current architecture

Source of truth: code under `src/` as of 2026-04-20.

The backend is no longer organized around six dedicated business services. The current runtime surface is:

- one root backend binary in `src/main.go`
- five supported modes: `producer`, `consumer`, `both`, `api-gateway`, `runtime-worker-service`
- two standalone backend binaries under `src/cmd/`: `api-gateway` and `runtime-worker-service`
- one remaining internal gRPC seam: `proto/runtime/v1`

| Mode | What starts | Typical use |
| --- | --- | --- |
| `producer` | HTTP/OpenAPI stack only | API, router, handler, Swagger work |
| `consumer` | runtime worker stack only | queue, controller, receiver, runtime-only debugging |
| `both` | HTTP + runtime worker in one process set | fastest local end-to-end debugging |
| `api-gateway` | dedicated HTTP process + `RuntimeIntakeService` gRPC | split-process API boundary validation |
| `runtime-worker-service` | dedicated runtime worker + `RuntimeService` gRPC | split-process worker validation |

The dedicated split-process deployment is therefore `api-gateway <-> runtime-worker-service`, not the older IAM/resource/orchestrator/system split.

## Runtime model

- `api-gateway` owns the full HTTP surface under `/api/v2` and serves the runtime-intake gRPC endpoint that worker processes use to write execution and injection state back into the API-owned graph.
- `runtime-worker-service` owns the async worker stack: Redis consumption, controller / receiver / worker interfaces, K8s / Helm / BuildKit / Chaos runtime actions, and runtime status gRPC.
- `producer`, `consumer`, and `both` remain supported as collocated developer modes.
- Module HTTP wiring is generated from `src/module/*` by `scripts/generate_http_modules.py`; the generated registry lives in `src/app/http_modules_gen.go`.
- Module self-registration uses five plugin points: `routes`, `permissions`, `role_grants`, `migrations`, and `task_executors`.

## Repository layout

| Path | Purpose |
| --- | --- |
| `src/` | Go backend, runtime worker, router, modules, infra |
| `src/cmd/aegisctl/` | operator CLI |
| `sdk/python/` | Python SDK |
| `helm/` | Helm chart for the backend stack |
| `manifests/` | Kubernetes manifests and environment-specific overlays |
| `scripts/` | bootstrap, publish, regression, and command tooling |
| `docs/` | AegisLab-local design notes |
| `../docs/` | repo-root architecture, deployment, and troubleshooting docs |

## Quick start

### 1. Start local infrastructure

From `AegisLab/`:

```bash
docker compose up -d redis mysql etcd jaeger buildkitd loki prometheus grafana
```

This starts infra only. It does not start the backend application.

### 2. Start the backend

Fast local API debugging:

```bash
cd src
go run . producer -conf ./config.dev.toml -port 8082
```

Fast local end-to-end debugging:

```bash
cd src
go run . both -conf ./config.dev.toml -port 8082
```

Split-process debugging with the current dedicated topology:

```bash
# terminal 1
cd src
go run ./cmd/api-gateway -conf ./config.dev.toml -port 8082

# terminal 2
cd src
go run ./cmd/runtime-worker-service -conf ./config.dev.toml
```

Docker Compose variant for the same split-process topology:

```bash
docker compose -f docker-compose.yaml -f docker-compose.microservices.yaml up \
  api-gateway runtime-worker-service
```

Useful local endpoints when HTTP is running:

- API root: `http://localhost:8082/api/v2`
- Swagger UI: `http://localhost:8082/docs/index.html`
- health endpoint: `http://localhost:8082/system/health`

## Configuration

The working sample config is `src/config.dev.toml`. Edit that file in place, or pass another config directory / file path with `-conf`.

Current runtime-topology keys that matter most:

```toml
[clients.runtime]
# api-gateway -> runtime-worker query channel
target = "localhost:9094"

[clients.runtime_intake]
# runtime-worker -> api-gateway write-back channel
target = "localhost:9096"

[runtime_worker.grpc]
addr = ":9094"

[api_gateway.intake.grpc]
addr = ":9096"
```

Notes:

- `runtime-worker-service` requires `clients.runtime_intake.target` or the legacy fallback `runtime_intake.grpc.target`.
- `clients.runtime.target` is optional unless something needs the query side of `RuntimeService`.
- The root config loader validates core fields such as `name`, `version`, `port`, `workspace`, `database.mysql.*`, `redis.host`, `jaeger.endpoint`, and selected `k8s.*` settings.

## Common workflows

- Build the CLI: `just build-aegisctl`
- Run staging-profile cluster deploy: `just run`
- Run the regression smoke path: `just test-regression`
- Bootstrap the broader cluster demo stack: `bash scripts/start.sh test`

`just run` and `scripts/start.sh test` are different flows:

- `just run` uses `skaffold` with the repo's staging profile.
- `scripts/start.sh test` bootstraps cluster dependencies such as Chaos Mesh, cert-manager, ClickStack, OTel Kube Stack, and demo workloads.

## Validation commands

These are the fastest repo-native checks for the current backend topology:

```bash
cd src
go test ./app -count=1
go test ./module -run TestModulePackagesAvoidForeignRepositoryConstructors -count=1
go test ./service/consumer -count=1
go test ./router ./interface/http -count=1
```

Phase-6 topology-specific checks:

- `src/app/http_modules_generated_test.go`
- `src/module/widget/registration_test.go`
- `src/module/boundary_test.go`
- `src/service/consumer/task_executors_test.go`

## Documentation map

Current docs to read first:

- [`../docs/code-topology/README.md`](../docs/code-topology/README.md) - current backend topology overview
- [`../docs/code-topology/slices/01-app-wiring.md`](../docs/code-topology/slices/01-app-wiring.md) - `fx` graphs and startup modes
- [`../docs/code-topology/slices/06-grpc-interfaces.md`](../docs/code-topology/slices/06-grpc-interfaces.md) - remaining runtime gRPC seam
- [`../docs/deployment/README.md`](../docs/deployment/README.md) - deployment and smoke-test entrypoint map
- [`../docs/troubleshooting/README.md`](../docs/troubleshooting/README.md) - cross-repo troubleshooting runbooks
- [`CONTRIBUTING.md`](CONTRIBUTING.md) - module self-registration and cross-module boundary rules
- [`src/cmd/aegisctl/README.md`](src/cmd/aegisctl/README.md) - CLI usage
- [`docs/inject-pipeline-design.md`](docs/inject-pipeline-design.md) - guided injection request pipeline

Notes on older docs:

- The repo-root `docs/code-topology/` directory contains some archival deep dives that predate the phase-2 collapse and phase-6 wiring cleanup.
- Treat `../docs/code-topology/README.md`, `slices/01-app-wiring.md`, and `slices/06-grpc-interfaces.md` as the authoritative topology docs.

## Troubleshooting shortcuts

- Current cross-repo troubleshooting index: [`../docs/troubleshooting/README.md`](../docs/troubleshooting/README.md)
- Local / cluster bootstrap runbook: [`../docs/troubleshooting/e2e-cluster-bootstrap.md`](../docs/troubleshooting/e2e-cluster-bootstrap.md)
- Latest repair log: [`../docs/troubleshooting/e2e-repair-record-2026-04-20.md`](../docs/troubleshooting/e2e-repair-record-2026-04-20.md)

## License

This project is licensed under Apache 2.0. See [`LICENSE`](LICENSE).
