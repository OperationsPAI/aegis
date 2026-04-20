# AegisLab Code Topology - Phase 6

Source of truth: code under `/home/ddq/AoyangSpace/aegis/AegisLab/src/` as of 2026-04-20.

This topology reflects the phase-2 gRPC collapse and the phase-3/4/6 self-registration work from issue `#28`.
The old 6-service split is no longer the primary architecture. Use this file plus
`docs/code-topology/slices/01-app-wiring.md` and
`docs/code-topology/slices/06-grpc-interfaces.md` first.

## Documents

Current, revalidated topology docs:

- `README.md` - current overview, process model, plugin points, validation hooks.
- `slices/01-app-wiring.md` - concrete `fx.Option` graphs for every supported mode.
- `slices/06-grpc-interfaces.md` - the only remaining internal gRPC seam.

Archival deep dives kept for provenance, not as the current topology source of truth:

- `flows.md`
- `reference.md`
- `orphans.md`
- `slices/02-infra.md`
- `slices/03-modules-iam.md`
- `slices/04-modules-resource.md`
- `slices/05-orchestration.md`
- `slices/07-data-crosscut-seams.md`

## 1. Runtime shapes

AegisLab now has two dedicated binaries and three legacy collocated modes.

| Shape | Entrypoint | `fx` graph | Purpose |
| --- | --- | --- | --- |
| `producer` | root `main.go` | `app.ProducerOptions` | Legacy all-in-one API mode |
| `consumer` | root `main.go` | `app.ConsumerOptions` | Legacy all-in-one worker mode |
| `both` | root `main.go` | `app.BothOptions` | Legacy local-debug mode |
| `api-gateway` | `cmd/api-gateway/main.go` or root `main.go` | `gateway.Options` | Dedicated API process |
| `runtime-worker-service` | `cmd/runtime-worker-service/main.go` or root `main.go` | `runtimeapp.Options` | Dedicated worker process |

The intended deployment shape is:

```text
Frontend / CLI / SDK
        |
        v
+--------------------+        shared DB/Redis/etcd/etc.        +-----------------------+
| api-gateway        | <-------------------------------------> | runtime-worker-service |
| - HTTP + Swagger   |                                         | - worker/controller    |
| - all modules      |                                         | - OTLP receiver        |
| - runtime intake   |                                         | - runtime status gRPC  |
+--------------------+                                         +-----------------------+
```

Removed from the primary topology:

- `iam-service`
- `orchestrator-service`
- `resource-service`
- `system-service`
- `internalclient/{iam,orchestrator,resource,system}client`
- gateway-side per-module remote shims

## 2. App composition

Common wiring is still split into small `app/*` building blocks:

| Function | Role |
| --- | --- |
| `app.BaseOptions(confPath)` | config + logger |
| `app.ObserveOptions()` | loki + tracing |
| `app.DataOptions()` | db + redis |
| `app.CoordinationOptions()` | etcd |
| `app.BuildInfraOptions()` | harbor + helm + buildkit |
| `app.CommonOptions(confPath)` | base + observe + data + coordination + build infra |

Dedicated processes compose those blocks like this:

- `gateway.Options(confPath, port)` = base + observe + data + coordination + build infra +
  `chaos.Module` + `k8s.Module` + `app.ProducerHTTPOptions(port)` + `grpcruntimeintake.Module`
- `runtimeapp.Options(confPath)` = base + observe + data + coordination + build infra +
  `app.ExecutionInjectionOwnerModules()` + `app.RuntimeWorkerStackOptions()` +
  `consumer.RemoteOwnerOptions()` + `app.RequireConfiguredTargets(api-gateway-intake)`

Legacy collocated modes are still supported:

- `app.ProducerOptions` = `app.CommonOptions` + `chaos.Module` + `k8s.Module` + HTTP stack
- `app.ConsumerOptions` = `app.CommonOptions` + runtime worker stack + local owner modules
- `app.BothOptions` = `app.CommonOptions` + runtime worker stack + HTTP stack

## 3. Module self-registration

Phase 3/4/6 turned module wiring into a plugin-style model. The framework now exposes five
`fx` value groups:

| Plugin point | Group tag | Aggregation site |
| --- | --- | --- |
| Routes | `group:"routes"` | `router.New(...)` |
| Permissions | `group:"permissions"` | `module/rbac.AggregatePermissions(...)` |
| Role grants | `group:"role_grants"` | `module/rbac.AggregatePermissions(...)` |
| Migrations | `group:"migrations"` | `infra/db.NewGormDB(...)` |
| Task executors | `group:"task_executors"` | `service/consumer.NewTaskRegistry(...)` |

Middleware is intentionally not self-registered; it remains centralized policy.

Every module is expected to expose a single `module.go` manifest and to contribute its own:

- `routes.go`
- `permissions.go`
- `migrations.go`
- optional task-executor fragments
- public `Reader` / `Writer` interfaces for cross-module access

Current proof points added during phase 6:

- `module/widget/` is a fake self-registering module used to prove zero-touch module addition.
- `app/http_modules_gen.go` is generated from the module directory tree by
  `AegisLab/scripts/generate_http_modules.py`.
- `app/http_modules_generated_test.go` fails if the generated registry drifts from the
  directories under `module/`.
- `module/boundary_test.go` fails if one module reaches into another module via
  `OtherModule.NewRepository(...)` or `OtherModule.Repository`.

## 4. HTTP module registries

There are now two important app-level registries:

1. `ExecutionInjectionOwnerModules()`
   - Used by local owner modes (`consumer`, `both`, and as a base in `runtimeapp.Options`).
   - Wires the modules needed for local execution/injection ownership:
     `container`, `dataset`, `execution`, `injection`, `label`.

2. `ProducerHTTPModules()`
   - Used by HTTP-serving modes.
   - Returns the generated module list from `app/http_modules_gen.go`.
   - Current generated module set:
     `auth`, `chaossystem`, `container`, `dataset`, `evaluation`, `execution`, `group`,
     `injection`, `label`, `metric`, `notification`, `pedestal`, `project`, `ratelimiter`,
     `rbac`, `sdk`, `system`, `systemmetric`, `task`, `team`, `trace`, `user`, `widget`,
     plus `router.Module`.

Because the HTTP list is generated, adding or removing a module directory no longer requires
editing a central registry file by hand.

## 5. Remaining gRPC boundary

Only one internal gRPC package family remains: `proto/runtime/v1`.

It defines two services in one proto file:

- `RuntimeService` - served by `runtime-worker-service`
- `RuntimeIntakeService` - served by `api-gateway`

This is the only preserved internal process boundary after the collapse.

- Query direction (`api-gateway` or tooling -> worker): runtime status, queue status,
  limiter status, namespace locks, queued tasks.
- Intake direction (`runtime-worker` -> api-gateway): create/update execution and injection
  records in the shared DB.

The shared client is `internalclient/runtimeclient.Client`, which has two independently
configurable targets:

- `clients.runtime.target` / legacy `runtime_worker.grpc.target`
- `clients.runtime_intake.target` / legacy `runtime_intake.grpc.target`

The dedicated runtime-worker process uses `consumer.RemoteOwnerOptions()` to decorate the
local owners so that execution/injection writes go through the intake client instead of
calling the local services directly.

## 6. What is still transitional

The codebase is closer to the target architecture, but a few coexistence mechanisms remain:

- `router.New(...)` still invokes the older centralized `Setup*V2Routes(...)` helpers, but the
  current helpers are compatibility no-op shims; module registrars do the real route mounting.
- `infra/db/migration.go` still has a `centralEntities()` slice alongside module-contributed
  migrations.
- `infra/db/migration.go` still seeds builtin systems centrally.
- The lifecycle helpers in `interface/http/server.go` and `interface/grpc/{runtime,runtimeintake}/lifecycle.go`
  still start their servers asynchronously in goroutines.

Those are important if you are evaluating later cleanup work, but they do not change the
high-level process model described above.

## 7. Phase 6 validation hooks

The phase-6 changes are now covered by a mix of focused and smoke tests:

- `AegisLab/src/app/http_modules_generated_test.go` - generated HTTP registry stays in sync.
- `AegisLab/src/module/widget/registration_test.go` - fake module contributes routes,
  permissions, and migrations.
- `AegisLab/src/module/boundary_test.go` - module packages cannot construct foreign repositories.
- `AegisLab/src/app/startup_validate_test.go` - legacy app graphs validate.
- `AegisLab/src/app/service_entrypoints_test.go` - dedicated `api-gateway` and
  `runtime-worker-service` graphs validate and start.
- `AegisLab/src/app/startup_smoke_test.go` - collocated modes still boot and expose the
  expected HTTP/runtime surfaces.
