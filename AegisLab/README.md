# AegisLab

Root-cause-analysis benchmarking platform for microservice systems. AegisLab
owns chaos systems, projects, datasets, fault injections, executions,
evaluations, and the async runtime that drives the build → inject → collect →
detect pipeline.

## Repository at a glance

```
AegisLab/
├── src/                       # single Go module (module path: aegis)
│   ├── main.go                # rcabench multi-mode entrypoint (cobra)
│   ├── cmd/                   # split-process binaries (aegis-api, aegis-gateway, ...)
│   ├── cli/                   # aegisctl operator CLI
│   ├── boot/                  # process wiring: fx Options per binary, lifecycle hooks
│   │   ├── monolith/  runtime/  gateway/  sso/  notify/  blob/  configcenter/
│   │   ├── wiring/http/       # shared HTTP server wiring used by every HTTP binary
│   │   └── seed/              # first-boot DB seeding (admin/RBAC/containers/datasets)
│   ├── core/                  # business core
│   │   ├── domain/            # bounded contexts: injection, execution, container,
│   │   │                      # dataset, task, group, chaossystem, pedestal
│   │   └── orchestrator/      # async pipeline (Redis queues, CRD callbacks,
│   │                          # build/inject/collect/detect stages, batch manager)
│   ├── crud/                  # supporting CRUD surfaces
│   │   ├── iam/               # user, auth, sso, rbac, label, team, project
│   │   ├── admin/             # configcenter, ratelimiter, widget
│   │   ├── messaging/         # notification
│   │   ├── observability/     # system, trace, metric, sdk, observation,
│   │   │                      # evaluation, systemmetric
│   │   └── storage/           # blob
│   ├── clients/               # outbound HTTP/gRPC clients
│   │   ├── gateway/           # L7 gateway router + auth + proxy pool
│   │   ├── sso/               # SSO/OIDC client (token, JWKS, permission check)
│   │   ├── runtime/           # gRPC client to runtime-worker-service
│   │   ├── notification/  blob/  configcenter/
│   ├── platform/              # framework + infrastructure (zero domain logic)
│   │   ├── config/            # viper loader + env mode (dev/test/prod)
│   │   ├── framework/         # plugin point interfaces (RouteRegistrar,
│   │   │                      # PermissionRegistrar, MigrationRegistrar, ...)
│   │   ├── db/  redis/  etcd/ # connection gateways
│   │   ├── router/  middleware/  httpx/   # gin router + middleware
│   │   ├── jwtkeys/           # RS256 sign/verify (signer + remote JWKS verifier)
│   │   ├── chaos/  k8s/  buildkit/  helm/  harbor/   # cluster integrations
│   │   ├── tracing/  logger/  metrics/    # observability
│   │   ├── consts/  dto/  model/  utils/  # cross-cutting types
│   │   └── ...
│   ├── docs/                  # generated swagger
│   ├── scripts/               # generate_http_modules.py, etc.
│   └── tools/                 # relayout.sh and other repo-maintenance tools
├── sdk/python/                # auto-generated Python SDK
├── helm/                      # Helm chart (rcabench + rcabench-frontend + rcabench-sso)
├── manifests/                 # env-specific overlays (local, staging, kind, byte-cluster)
├── scripts/                   # bootstrap, regression, publish helpers (uv-managed)
├── data/                      # initial seed data (initial_data/{prod,staging}/*.yaml)
└── CLAUDE.md  CONTRIBUTING.md  README.md
```

## Design model

**Modular monolith, configured per binary.** One Go module, one schema, one
chart — but `cmd/aegis-*` binaries each link only the `boot/<role>` Options
they need. The same module code runs in either the `rcabench both` developer
process or as a split-process gateway + worker + sso + ... deployment.

**Layered dependencies (downward only).**

```
cmd/        ─────  thin main() that picks a boot profile
boot/       ─────  fx Options: which modules + lifecycle hooks
core/       ─────  domain + orchestrator (uses platform, never imports boot)
crud/       ─────  supporting CRUD modules (uses platform)
clients/    ─────  outbound clients (sso, runtime, gateway, ...) — leaf layer
platform/   ─────  framework + infra; depended on by everything, depends on nothing
```

`core` and `crud` are siblings: both depend only on `platform`. `boot`
wires them together. `clients` are leaves used by `boot` and `core` callers.

**Plugin-point composition via `fx` value-groups.** Every domain/CRUD
package exposes a `Module = fx.Module(...)` and contributes to five
framework plugin points:

| Group              | Aggregated by                          | What it adds                  |
| ------------------ | -------------------------------------- | ----------------------------- |
| `routes`           | `platform/router.New`                  | gin route registration        |
| `permissions`      | `crud/iam/rbac.AggregatePermissions`   | RBAC permission definitions   |
| `role_grants`      | `crud/iam/rbac.AggregatePermissions`   | role → permission bindings    |
| `migrations`       | `platform/db.NewGormDB`                | GORM AutoMigrate entities     |
| `task_executors`   | `core/orchestrator/common`             | async task handlers           |

A new package never edits a central registry; it only adds its `fx.Module`
to `boot/wiring/http_modules_gen.go` (which is **generated** — see
"Adding a new module" below).

## Quick start

```bash
# 0. one-time
make check-prerequisites
eval "$(devbox shellenv)"

# 1. infra
docker compose up -d redis mysql etcd jaeger buildkitd loki prometheus grafana

# 2. backend (fastest end-to-end loop)
cd src
go run -tags duckdb_arrow . both -conf ./config.dev.toml -port 8082

# Swagger: http://localhost:8082/docs/index.html
# Health:  http://localhost:8082/system/health
```

For a real K8s deployment (run from the monorepo root, not AegisLab/):

```bash
just sso-keys                  # one-time RSA keypair under data/sso/
cd ..                          # monorepo root holds the unified skaffold.yaml
skaffold run -p local          # build all 5 images + helm install into current kubectx
```

Common workflows: `just build-aegisctl`, `just run`, `just test-regression`,
`bash scripts/start.sh test` (bootstraps Chaos Mesh + cert-manager +
OTel Kube Stack + demo workloads).

## Multi-mode entrypoints

`src/main.go` is a cobra root with six modes — historical convenience for
developers. Production deployments use the split `cmd/aegis-*` binaries
behind the chart.

| Mode (rcabench)              | Equivalent split binary       | Purpose                                       |
| ---------------------------- | ----------------------------- | --------------------------------------------- |
| `producer`                   | (HTTP-only subset of aegis-api) | API server, no async workers                |
| `consumer`                   | runtime-worker-service        | async pipeline only                           |
| `both`                       | aegis-api (same boot)         | API + workers in one process (default for dev) |
| `aegis-api`                  | `cmd/aegis-api`               | full monolith (HTTP + runtime in one)         |
| `runtime-worker-service`     | `cmd/runtime-worker-service`  | dedicated async worker                        |
| `sso`                        | `cmd/aegis-sso`               | identity service (port 8083)                  |

The chart deploys: `aegis-api`, `aegis-gateway`, `aegis-sso`,
`aegis-notify`, `aegis-blob`, `aegis-configcenter`,
`runtime-worker-service`, plus infra (mysql, redis, etcd, jaeger, ...).
The gateway terminates external traffic and fans out to the per-service
backends.

## Navigating the code

**By question:**

| If you want to … | Start at |
| --- | --- |
| understand startup wiring for a binary | `src/boot/<role>/options.go` |
| find which packages a binary links | `src/boot/<role>/options.go` + `boot/http_modules_gen.go` |
| trace an HTTP request | `platform/router/router.go` → module's `routes.go` → handler |
| read pipeline orchestration | `src/core/orchestrator/{fault_injection,build_datapack,algo_execution,collect_result}.go` |
| understand CRD callback flow | `core/orchestrator/k8s_handler.go` (HandleCRDSucceeded / HandleJobSucceeded) |
| see what gets seeded on first boot | `src/boot/seed/producer.go`, `src/boot/seed/permissions.go` |
| change L7 routing | `helm/templates/configmap.yaml` `[[gateway.routes]]` blocks |
| swap SSO client behavior | `src/clients/sso/{client.go,module.go}` |
| add or change a DB entity | the entity's `crud/<group>/<name>/migrations.go` (auto-registered) |

**By layer (search prefix):**

```bash
# any business operation:
rg "func \(s \*Service\)"  src/core/domain/<name>/
rg "func \(h \*Handler\)"  src/core/domain/<name>/
rg "func \(r \*Repository\)" src/core/domain/<name>/
```

**By package boundaries:** `core` is allowed to import `platform` and
`clients`. It is **not** allowed to import `boot` or `cmd`.
`src/core/domain/boundary_test.go` enforces that. The cycle-check passes
in CI.

## Adding a new module

A "module" is any self-contained CRUD or domain surface (`core/domain/<x>`
or `crud/<group>/<x>`). Adding one should require **zero edits** to other
modules.

1. **Create the directory** under the right layer:
   - business-pipeline-shaped data → `src/core/domain/<name>/`
   - supporting CRUD → `src/crud/<group>/<name>/`
2. **Standard files:**
   - `module.go` — exposes `var Module = fx.Module("<name>", ...)`
   - `migrations.go` — returns a `framework.MigrationRegistrar` if the
     module owns DB tables
   - `service.go`, `repository.go`, `handler.go`, `routes.go`,
     `permissions.go` — only the ones you actually need
3. **Register plugin contributions** inside `module.go` using fx group tags:
   ```go
   var Module = fx.Module("foo",
       fx.Provide(NewRepository, NewService, NewHandler),
       fx.Provide(
           fx.Annotate(Routes,       fx.ResultTags(`group:"routes"`)),
           fx.Annotate(Permissions,  fx.ResultTags(`group:"permissions"`)),
           fx.Annotate(Migrations,   fx.ResultTags(`group:"migrations"`)),
       ),
   )
   ```
4. **Regenerate the HTTP module index** so monolith + aegis-api pick the
   module up:
   ```bash
   python3 src/scripts/generate_http_modules.py
   ```
   That rewrites `src/boot/http_modules_gen.go`. **Never hand-edit that
   file** — it's reproducible from the directory walk.
5. **(Optional) link into other roles.** If a non-monolith binary also
   needs the module (e.g., aegis-gateway), add it to that binary's
   `boot/<role>/options.go`.
6. **Regenerate the SDK** if you added public API:
   ```bash
   make swag-init
   make generate-typescript-sdk SDK_VERSION=0.0.0
   make generate-python-sdk
   ```

What you do **not** need to edit: any other module, any router/registry
central file, the chart, or the gateway route table (unless your module
mounts on a path the gateway doesn't already wildcard).

## Configuration

`src/config.dev.toml` is the working sample. `ENV` selects the profile
(`dev` is the default; `prod` enables fail-closed behavior — empty
`gateway.trusted_header_key` becomes fatal, dev fallbacks disappear,
etc.).

Required keys for split-process deployments:

```toml
[clients.runtime]
target = "runtime-worker-service:9094"      # api-gateway → worker queries

[clients.runtime_intake]
target = "aegis-api:9096"                   # worker → api-gateway write-back

[runtime_worker.grpc]
addr = ":9094"

[api_gateway.intake.grpc]
addr = ":9096"
```

`platform/config` validates `name`, `version`, `port`, `workspace`,
`database.mysql.*`, `redis.host`, `jaeger.endpoint`, and selected
`k8s.*` settings at startup.

## Validation gates (per CLAUDE.md)

```bash
# Backend
cd src
go build -tags duckdb_arrow -o /dev/null ./main.go
golangci-lint run
go test ./platform/... -count=1

# Boundary + wiring smoke tests
go test ./boot -count=1
go test ./core/domain -run TestDomainAvoidsBootImports -count=1

# Full deploy smoke (from monorepo root)
( cd .. && skaffold run -p local )
```

The `aegisctl schema diff gate` CI workflow rebuilds aegisctl on both
PR base and head and fails if the CLI surface changes without
`schema-changes-acknowledged: true` in the PR body.

## Documentation

This README + [`CONTRIBUTING.md`](CONTRIBUTING.md) + [`src/cli/README.md`](src/cli/README.md)
are the current entry points. Cross-repo runbooks live in `../docs/`:

- `../docs/code-topology/` — module call paths, dead-code map
- `../docs/troubleshooting/benchmark-integration-playbook.md` — fresh-cluster pitfalls
- `../docs/troubleshooting/datapack-schema.md` — parquet schema reference
- `../CLAUDE.md` — workspace-wide working guidelines

Legacy in-repo design docs that drove past migrations (`sso-extraction-*`,
`inject-pipeline-design`, `frontend-*`) have been removed; the code is
the source of truth for current behavior.

## Validation status of top-level files

Anchored to the 2026-05-12 cold-start kind walkthrough
(see [`../docs/deployment/cold-start-kind.md`](../docs/deployment/cold-start-kind.md)).
Subdirectories are labelled individually with `LEGACY.md` / `VALIDATION.md`
where relevant.

| File | Status | Notes |
|---|---|---|
| `README.md`, `CONTRIBUTING.md`, `CHANGELOG.md`, `LICENSE` | validated 2026-05-12 | Current documentation entry points; CHANGELOG is auto-generated by `cliff.toml`. |
| `CLAUDE.md` | validated 2026-05-12 | Project guidelines actively used. |
| `justfile` | partial | Recipes that wrap the validated flow (`build-aegisctl`, `swag-init`, `sso-keys`) work; many other recipes target legacy / cloud paths and are not exercised. |
| `devbox.json` / `devbox.lock` | validated 2026-05-12 | Required for the documented dev shell. |
| `project-index.yaml` | live | Spec index referenced by CLAUDE.md north-star targets. |
| `lefthook.yml` | live | Pre-commit hooks; runs in dev shell. |
| `cliff.toml` | live | Used by release tooling. |
| `config.dev.toml` | partial | Loaded by `src/main.go both` for local-debug (legacy Docker-compose path); the validated kind flow injects config through the helm chart instead. |
| `Caddyfile.dev` | **legacy** | Reverse-proxy config for an older local-dev stack with a separate frontend. The kind flow does not run Caddy. |
| `docker-compose.yaml` | **legacy** | Brings up redis/mysql/jaeger/buildkitd for the bare-metal `make local-debug` loop. The validated cold-start flow runs everything in-cluster (helm chart provides redis/mysql/etcd; otel-kube-stack provides jaeger). |
| `docker-compose.microservices.yaml` | **legacy** | Compose-based benchmark deployments superseded by helm charts. |
| `skaffold.yaml` | **removed** | Unified into the monorepo-root `../skaffold.yaml` which now builds all 5 images (rcabench-platform, clickhouse_dataset, reason, detector, rcabench) and drives helm install. Run from the monorepo root. |

"Legacy" here means **not exercised by the validated cold-start flow** — the
file may still work, but no run since 2026-04-18 (repo relayout `e6cb801`)
proves it. Verify and update this table before relying on a legacy entry.

## License

Apache 2.0 — see [`LICENSE`](LICENSE).
