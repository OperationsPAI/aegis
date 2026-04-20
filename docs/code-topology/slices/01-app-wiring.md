# 01 · App wiring (Phase 6)

Source: `/home/ddq/AoyangSpace/aegis/AegisLab/src`

This slice replaces the old 6-service app-wiring inventory. The current architecture has two
primary dedicated processes (`api-gateway` and `runtime-worker-service`) plus three legacy
collocated modes (`producer`, `consumer`, `both`).

## 1. Entrypoints and modes

| Mode | Entrypoint | `fx` graph | Notes |
| --- | --- | --- | --- |
| `producer` | root `main.go` | `app.ProducerOptions(conf, port)` | Legacy monolithic API mode |
| `consumer` | root `main.go` | `app.ConsumerOptions(conf)` | Legacy monolithic worker mode |
| `both` | root `main.go` | `app.BothOptions(conf, port)` | Legacy local-debug mode |
| `api-gateway` | `cmd/api-gateway/main.go` or root `main.go` | `gateway.Options(conf, port)` | Dedicated API process |
| `runtime-worker-service` | `cmd/runtime-worker-service/main.go` or root `main.go` | `runtimeapp.Options(conf)` | Dedicated worker process |

There are no dedicated `iam-service`, `resource-service`, `orchestrator-service`, or
`system-service` binaries in `cmd/` anymore.

## 2. Shared building blocks

| Function | Source | Purpose |
| --- | --- | --- |
| `app.BaseOptions(confPath)` | `app/app.go` | config + logger |
| `app.ObserveOptions()` | `app/app.go` | loki + tracing |
| `app.DataOptions()` | `app/app.go` | db + redis |
| `app.CoordinationOptions()` | `app/app.go` | etcd |
| `app.BuildInfraOptions()` | `app/app.go` | harbor + helm + buildkit |
| `app.CommonOptions(confPath)` | `app/app.go` | base + observe + data + coordination + build infra |

## 3. Option graphs

### 3.1 `app.ProducerOptions(confPath, port)`

```text
app.CommonOptions(confPath)
+ chaos.Module
+ k8s.Module
+ app.ProducerHTTPOptions(port)
```

`app.ProducerHTTPOptions(port)` expands to:

```text
fx.Provide(newProducerInitializer)
+ fx.Invoke(registerProducerInitialization)
+ app.ProducerHTTPModules()
+ fx.Supply(httpapi.ServerConfig{Addr: normalizeAddr(port)})
+ httpapi.Module
```

### 3.2 `app.ConsumerOptions(confPath)`

```text
app.CommonOptions(confPath)
+ app.RuntimeWorkerStackOptions()
+ app.ExecutionInjectionOwnerModules()
```

This is the local-owner worker mode: execution/injection writes stay in-process.

### 3.3 `app.BothOptions(confPath, port)`

```text
app.CommonOptions(confPath)
+ app.RuntimeWorkerStackOptions()
+ app.ProducerHTTPOptions(port)
```

`both` gets local execution/injection ownership transitively because
`app.ProducerHTTPModules()` already includes those modules.

### 3.4 `gateway.Options(confPath, port)`

```text
app.BaseOptions(confPath)
+ app.ObserveOptions()
+ app.DataOptions()
+ app.CoordinationOptions()
+ app.BuildInfraOptions()
+ chaos.Module
+ k8s.Module
+ app.ProducerHTTPOptions(port)
+ grpcruntimeintake.Module
```

Important differences vs. the old gateway graph:

- no per-module remote shims
- no `iamclient`, `resourceclient`, `orchestratorclient`, or `systemclient`
- no large `fx.Decorate(...)` table for `HandlerService` fan-out

The API process now owns all modules locally and only exposes the runtime-intake gRPC server
for worker callbacks.

### 3.5 `runtimeapp.Options(confPath)`

```text
app.BaseOptions(confPath)
+ app.ObserveOptions()
+ app.DataOptions()
+ app.CoordinationOptions()
+ app.BuildInfraOptions()
+ app.ExecutionInjectionOwnerModules()
+ app.RuntimeWorkerStackOptions()
+ consumer.RemoteOwnerOptions()
+ app.RequireConfiguredTargets(
    "runtime-worker-service",
    RequiredConfigTarget{
      Name: "api-gateway-intake",
      PrimaryKey: "clients.runtime_intake.target",
      LegacyKey: "runtime_intake.grpc.target",
    },
  )
```

This is the only graph that still uses `fx.Decorate(...)`: `consumer.RemoteOwnerOptions()`
replaces the local execution/injection owners with runtime-intake gRPC-backed owners.

## 4. Registry helpers

### `app.ExecutionInjectionOwnerModules()`

Current contents:

```text
container.Module
+ dataset.Module
+ execution.Module
+ injection.Module
+ label.Module
```

Phase 6 expanded this registry so local worker graphs still satisfy the new cross-module
`Reader` / `Writer` dependencies introduced in `execution.Service` and `injection.Service`.

### `app.ProducerHTTPModules()`

This registry is generated, not hand-maintained.

- Source wrapper: `app/http_modules.go`
- Generated file: `app/http_modules_gen.go`
- Generator: `AegisLab/scripts/generate_http_modules.py`
- Sync test: `app/http_modules_generated_test.go`

Generated module set:

```text
auth, chaossystem, container, dataset, evaluation, execution, group,
injection, label, metric, notification, pedestal, project, ratelimiter,
rbac, sdk, system, systemmetric, task, team, trace, user, widget,
router.Module
```

## 5. Runtime worker stack

`app.RuntimeWorkerStackOptions()` provides the shared async runtime plumbing:

```text
runtimeinfra.Module
+ chaos.Module
+ k8s.Module
+ runtimeclient.Module
+ consumer.Module
+ fx.Provide(
    consumer.NewMonitor,
    restart/build/algo rate limiters,
    consumer.NewFaultBatchManager,
    consumer.NewExecutionOwner,
    consumer.NewInjectionOwner,
  )
+ worker.Module
+ controller.Module
+ grpcruntime.Module
+ receiver.Module
```

Notes:

- In collocated modes (`consumer`, `both`) the owners stay local.
- In dedicated `runtime-worker-service`, `consumer.RemoteOwnerOptions()` decorates those owners
  to use the runtime-intake gRPC client.
- `runtimeclient.Module` is present in the shared stack because the worker process owns the
  intake client configuration; the query side remains optional.

## 6. Self-registration aggregation sites

| Group | Produced by modules | Aggregated at |
| --- | --- | --- |
| `routes` | `module/*/routes.go` | `router.New(...)` |
| `permissions` | `module/*/permissions.go` | `module/rbac.AggregatePermissions(...)` |
| `role_grants` | `module/*/permissions.go` | `module/rbac.AggregatePermissions(...)` |
| `migrations` | `module/*/migrations.go` | `infra/db.NewGormDB(...)` |
| `task_executors` | `service/consumer/task_executors.go` and future modules | `service/consumer.NewTaskRegistry(...)` |

The app layer no longer needs hand-edited per-module routing tables to add a new module.

## 7. Validation coverage

The wiring changes are backed by:

- `app/startup_validate_test.go` - validates `producer`, `consumer`, and `both`
- `app/service_entrypoints_test.go` - validates dedicated `gateway` and `runtimeapp` graphs
- `app/startup_smoke_test.go` - starts/stops collocated graphs and checks exposed surfaces
- `app/http_modules_generated_test.go` - prevents generated registry drift

These tests are the quickest way to verify future wiring changes after adding or removing a
module.
