# 01 · App composition & entry points

Source: `/home/ddq/AoyangSpace/aegis/AegisLab/src`

---

## 1. The 9 run modes

| # | Mode | `cmd/X/main.go` entrypoint | `app.XxxOptions` producing the fx graph | Dispatched from `main.go` |
|---|---|---|---|---|
| 1 | `producer` (legacy) | — (only via root `main.go`) | `app.ProducerOptions(confPath, port)` — `app/producer.go:19` | `main.go:68-70` |
| 2 | `consumer` (legacy) | — | `app.ConsumerOptions(confPath)` — `app/consumer.go:5` | `main.go:71-73` |
| 3 | `both` (legacy) | — | `app.BothOptions(confPath, port)` — `app/both.go:5` | `main.go:74-76` |
| 4 | `api-gateway` | `cmd/api-gateway/main.go:11-17` | `gateway.Options(confPath, port)` — `app/gateway/options.go:36` | `main.go:77-79` |
| 5 | `iam-service` | `cmd/iam-service/main.go:11-16` | `iam.Options(confPath)` — `app/iam/options.go:17` | `main.go:80-82` |
| 6 | `orchestrator-service` | `cmd/orchestrator-service/main.go:11-16` | `orchestrator.Options(confPath)` — `app/orchestrator/options.go:16` | `main.go:83-85` |
| 7 | `resource-service` | `cmd/resource-service/main.go:11-16` | `resource.Options(confPath)` — `app/resource/options.go:18` | `main.go:86-88` |
| 8 | `runtime-worker-service` | `cmd/runtime-worker-service/main.go:11-16` | `runtimeapp.Options(confPath)` — `app/runtime/options.go:11` | `main.go:89-91` |
| 9 | `system-service` | `cmd/system-service/main.go:11-16` | `system.Options(confPath)` — `app/system/options.go:15` | `main.go:92-94` |

Each `cmd/*/main.go` is a thin `flag.Parse()` → `fx.New(...).Run()` wrapper. Root `main.go` uses cobra + viper to dispatch the same options.

---

## 2. fx building blocks (from `app/app.go`)

| Function | file:line | Modules wired |
|---|---|---|
| `BaseOptions(confPath)` | `app/app.go:18` | `fx.Supply(config.Params{Path})`, `config.Module`, `logger.Module` |
| `ObserveOptions()` | `app/app.go:26` | `loki.Module`, `tracing.Module` |
| `DataOptions()` | `app/app.go:33` | `db.Module`, `redis.Module` |
| `CoordinationOptions()` | `app/app.go:40` | `etcd.Module` |
| `BuildInfraOptions()` | `app/app.go:46` | `harbor.Module`, `helm.Module`, `buildkit.Module` |
| `CommonOptions(confPath)` | `app/app.go:54` | `BaseOptions + ObserveOptions + DataOptions + CoordinationOptions + BuildInfraOptions` |

Additional helpers:

| Function | file:line | Purpose |
|---|---|---|
| `ProducerHTTPOptions(port)` | `app/producer.go:28` | `fx.Provide(newProducerInitializer)` + `fx.Invoke(registerProducerInitialization)` + `ProducerHTTPModules()` + `fx.Supply(httpapi.ServerConfig{Addr})` + `httpapi.Module` |
| `ProducerHTTPModules()` | `app/http_modules.go:38` | 21 domain modules + `router.Module` (see §4) |
| `ExecutionInjectionOwnerModules()` | `app/http_modules.go:31` | `execution.Module`, `injection.Module` |
| `RuntimeWorkerStackOptions()` | `app/runtime_stack.go:17` | see §3 / runtime worker stack |
| `RequireConfiguredTargets(component, targets...)` | `app/remote_require.go:19` | fx.Invoke lifecycle hook that asserts required grpc-client targets are in config |

---

## 3. Per-mode composition table

Ordered list of fx Options wired per mode.

### 3.1 `producer` — `app/producer.go:19`
```
ProducerOptions =
  CommonOptions(conf)                        app/producer.go:21
  + chaos.Module                              app/producer.go:22
  + k8s.Module                                app/producer.go:23
  + ProducerHTTPOptions(port)                 app/producer.go:24
      ├─ fx.Provide(newProducerInitializer)   app/producer.go:30
      ├─ fx.Invoke(registerProducerInitialization)  app/producer.go:31
      ├─ ProducerHTTPModules()                app/producer.go:32
      ├─ fx.Supply(httpapi.ServerConfig{Addr:normalizeAddr(port)})  app/producer.go:33
      └─ httpapi.Module                       app/producer.go:34
```

### 3.2 `consumer` — `app/consumer.go:5`
```
ConsumerOptions =
  CommonOptions(conf)                        app/consumer.go:7
  + RuntimeWorkerStackOptions()               app/consumer.go:8
  + ExecutionInjectionOwnerModules()          app/consumer.go:9
```

### 3.3 `both` — `app/both.go:5`
```
BothOptions =
  CommonOptions(conf)                        app/both.go:7
  + RuntimeWorkerStackOptions()               app/both.go:8
  + ProducerHTTPOptions(port)                 app/both.go:9
```
(Note: no explicit `ExecutionInjectionOwnerModules()` line here — but it is transitively pulled in via `ProducerHTTPModules()` at `app/http_modules.go:45`.)

### 3.4 `api-gateway` — `app/gateway/options.go:36`
```
gateway.Options =
  app.BaseOptions(conf)                       options.go:38
  + app.ObserveOptions()                      options.go:39
  + app.DataOptions()                         options.go:40
  + app.CoordinationOptions()                 options.go:41
  + app.BuildInfraOptions()                   options.go:42
  + chaos.Module                              options.go:43
  + k8s.Module                                options.go:44
  + app.ProducerHTTPOptions(port)             options.go:45
  + app.RequireConfiguredTargets(...)         options.go:46-52   (iam/orchestrator/resource/system)
  + iamclient.Module                          options.go:53
  + orchestratorclient.Module                 options.go:54
  + resourceclient.Module                     options.go:55
  + systemclient.Module                       options.go:56
  + 19 × fx.Decorate(remoteAwareXxxService)   options.go:57-177
```

### 3.5 `iam-service` — `app/iam/options.go:17`
```
iam.Options =
  app.BaseOptions(conf)                       iam/options.go:19
  + app.ObserveOptions()                      iam/options.go:20
  + app.DataOptions()                         iam/options.go:21
  + app.RequireConfiguredTargets("iam-service", resource-service)   iam/options.go:22-25
  + resourceclient.Module                     iam/options.go:26
  + team.RemoteProjectReaderOption()          iam/options.go:27   (forces gRPC path)
  + auth.Module                               iam/options.go:28
  + rbac.Module                               iam/options.go:29
  + team.Module                               iam/options.go:30
  + user.Module                               iam/options.go:31
  + fx.Provide(middleware.NewService)         iam/options.go:32
  + grpciam.Module                            iam/options.go:33
```

### 3.6 `orchestrator-service` — `app/orchestrator/options.go:16`
```
orchestrator.Options =
  app.BaseOptions(conf)                       orchestrator/options.go:18
  + app.ObserveOptions()                      orchestrator/options.go:19
  + app.DataOptions()                         orchestrator/options.go:20
  + app.ExecutionInjectionOwnerModules()      orchestrator/options.go:21
  + group.Module                              orchestrator/options.go:22
  + metric.Module                             orchestrator/options.go:23
  + notification.Module                       orchestrator/options.go:24
  + task.Module                               orchestrator/options.go:25
  + trace.Module                              orchestrator/options.go:26
  + grpcorchestrator.Module                   orchestrator/options.go:27
```
(Note: no `CoordinationOptions`, no `BuildInfraOptions`, no gRPC client modules — orchestrator is a "leaf" service.)

### 3.7 `resource-service` — `app/resource/options.go:18`
```
resource.Options =
  app.BaseOptions(conf)                       resource/options.go:20
  + app.ObserveOptions()                      resource/options.go:21
  + app.DataOptions()                         resource/options.go:22
  + app.RequireConfiguredTargets("resource-service", orchestrator-service)  resource/options.go:23-26
  + orchestratorclient.Module                 resource/options.go:27
  + evaluation.RemoteQueryOption()            resource/options.go:28  (forces gRPC)
  + project.RemoteStatisticsOption()          resource/options.go:29  (forces gRPC)
  + chaossystem.Module                        resource/options.go:30
  + container.Module                          resource/options.go:31
  + dataset.Module                            resource/options.go:32
  + evaluation.Module                         resource/options.go:33
  + label.Module                              resource/options.go:34
  + project.Module                            resource/options.go:35
  + grpcresource.Module                       resource/options.go:36
```

### 3.8 `runtime-worker-service` — `app/runtime/options.go:11`
```
runtimeapp.Options =
  app.BaseOptions(conf)                       runtime/options.go:13
  + app.ObserveOptions()                      runtime/options.go:14
  + app.DataOptions()                         runtime/options.go:15
  + app.CoordinationOptions()                 runtime/options.go:16
  + app.BuildInfraOptions()                   runtime/options.go:17
  + app.RuntimeWorkerStackOptions()           runtime/options.go:18  (see below)
  + consumer.RemoteOwnerOptions()             runtime/options.go:19  (forces gRPC owner path)
  + app.RequireConfiguredTargets("runtime-worker-service", orchestrator-service)  runtime/options.go:20-23
```
`RuntimeWorkerStackOptions()` (`app/runtime_stack.go:17`) expands to:
```
runtimeinfra.Module                           runtime_stack.go:19
+ chaos.Module                                runtime_stack.go:20
+ k8s.Module                                  runtime_stack.go:21
+ orchestratorclient.Module                   runtime_stack.go:22
+ fx.Provide(                                 runtime_stack.go:23-31
    consumer.NewMonitor,
    fx.Annotate(consumer.NewRestartPedestalRateLimiter, fx.ResultTags(`name:"restart_limiter"`)),
    fx.Annotate(consumer.NewBuildContainerRateLimiter, fx.ResultTags(`name:"build_limiter"`)),
    fx.Annotate(consumer.NewAlgoExecutionRateLimiter,  fx.ResultTags(`name:"algo_limiter"`)),
    consumer.NewFaultBatchManager,
    consumer.NewExecutionOwner,
    consumer.NewInjectionOwner,
  )
+ worker.Module                               runtime_stack.go:32
+ controller.Module                           runtime_stack.go:33
+ grpcruntime.Module                          runtime_stack.go:34
+ receiver.Module                             runtime_stack.go:35
```

### 3.9 `system-service` — `app/system/options.go:15`
```
system.Options =
  app.BaseOptions(conf)                       system/options.go:17
  + app.ObserveOptions()                      system/options.go:18
  + app.DataOptions()                         system/options.go:19
  + app.CoordinationOptions()                 system/options.go:20
  + app.BuildInfraOptions()                   system/options.go:21
  + app.RequireConfiguredTargets("system-service", runtime-worker-service)  system/options.go:22-25
  + system.RemoteRuntimeQueryOption()         system/options.go:26
  + k8s.Module                                system/options.go:27
  + runtimeclient.Module                      system/options.go:28
  + system.Module                             system/options.go:29
  + systemmetric.Module                       system/options.go:30
  + grpcsystem.Module                         system/options.go:31
```

---

## 4. Module inventory wired into HTTP

### `ProducerHTTPModules()` — `app/http_modules.go:38-63`
21 module.Module invocations + router.Module:

| Module | file:line | Overridden by gateway Decorator? |
|---|---|---|
| `auth.Module` | http_modules.go:40 | yes → iam |
| `chaossystem.Module` | http_modules.go:41 | yes → resource |
| `container.Module` | http_modules.go:42 | yes → resource |
| `dataset.Module` | http_modules.go:43 | yes → resource |
| `evaluation.Module` | http_modules.go:44 | yes → resource |
| `execution.Module` (via `ExecutionInjectionOwnerModules`) | http_modules.go:45 / http_modules.go:33 | yes → orchestrator |
| `injection.Module` (via `ExecutionInjectionOwnerModules`) | http_modules.go:45 / http_modules.go:34 | yes → orchestrator |
| `group.Module` | http_modules.go:46 | yes → orchestrator |
| `label.Module` | http_modules.go:47 | yes → resource |
| `metric.Module` | http_modules.go:48 | yes → orchestrator+resource |
| `notification.Module` | http_modules.go:49 | yes → orchestrator |
| `pedestal.Module` | http_modules.go:50 | **no** (local-only) |
| `project.Module` | http_modules.go:51 | yes → resource |
| `ratelimiter.Module` | http_modules.go:52 | **no** (local-only) |
| `rbac.Module` | http_modules.go:53 | yes → iam |
| `sdk.Module` | http_modules.go:54 | **no** (local-only) |
| `system.Module` | http_modules.go:55 | yes → system |
| `systemmetric.Module` | http_modules.go:56 | yes → system |
| `task.Module` | http_modules.go:57 | yes → orchestrator |
| `team.Module` | http_modules.go:58 | yes → iam |
| `trace.Module` | http_modules.go:59 | yes → orchestrator |
| `user.Module` | http_modules.go:60 | yes → iam |
| `router.Module` | http_modules.go:61 | — |

### `gateway.Options()` additions (`app/gateway/options.go`)
Adds 4 internalclient Modules (`iamclient`, `orchestratorclient`, `resourceclient`, `systemclient`) plus 19 `fx.Decorate` wrappers — see §5. Also the gateway re-decorates the `middleware.Service` produced by `router.Module` via `remoteAwareMiddlewareService` (gateway/options.go:63-68).

Local-only modules that remain untouched at the gateway: `pedestal`, `ratelimiter`, `sdk`.

---

## 5. Gateway Decorator pattern

All from `app/gateway/options.go:57-177` (one `fx.Decorate` each). Wrapper structs live in `app/gateway/*_services.go`.

| # | Domain module | Local iface | gRPC client it routes to | Wrapper struct | File |
|---|---|---|---|---|---|
| 1 | auth | `auth.HandlerService` | `*iamclient.Client` | `remoteAwareAuthService` | `gateway/auth_services.go:29` |
| 2 | middleware | `middleware.Service` | `*iamclient.Client` | `remoteAwareMiddlewareService` | `gateway/middleware_service.go:13` |
| 3 | user | `user.HandlerService` | `*iamclient.Client` | `remoteAwareUserService` | `gateway/user_services.go:29` |
| 4 | rbac | `rbac.HandlerService` | `*iamclient.Client` | `remoteAwareRBACService` | `gateway/rbac_services.go:28` |
| 5 | team | `team.HandlerService` | `*iamclient.Client` | `remoteAwareTeamService` | `gateway/team_services.go:24` |
| 6 | execution | `execution.HandlerService` | `*orchestratorclient.Client` | `remoteAwareExecutionService` | `gateway/orchestrator_services.go:22` |
| 7 | injection | `injection.HandlerService` | `*orchestratorclient.Client` | `remoteAwareInjectionService` | `gateway/orchestrator_services.go:34` |
| 8 | task | `task.HandlerService` | `*orchestratorclient.Client` | `remoteAwareTaskService` | `gateway/orchestrator_services.go:68` |
| 9 | trace | `trace.HandlerService` | `*orchestratorclient.Client` | `remoteAwareTraceService` | `gateway/orchestrator_services.go:249` |
| 10 | group | `group.HandlerService` | `*orchestratorclient.Client` | `remoteAwareGroupService` | `gateway/orchestrator_services.go:293` |
| 11 | notification | `notification.HandlerService` | `*orchestratorclient.Client` | `remoteAwareNotificationService` | `gateway/orchestrator_services.go:328` |
| 12 | project | `project.HandlerService` | `*resourceclient.Client` | `remoteAwareProjectService` | `gateway/resource_services.go:16` |
| 13 | container | `container.HandlerService` | `*resourceclient.Client` | `remoteAwareContainerService` | `gateway/resource_services.go:35` |
| 14 | dataset | `dataset.HandlerService` | `*resourceclient.Client` | `remoteAwareDatasetService` | `gateway/resource_services.go:54` |
| 15 | evaluation | `evaluation.HandlerService` | `*resourceclient.Client` | `remoteAwareEvaluationService` | `gateway/resource_services.go:73` |
| 16 | label | `label.HandlerService` | `*resourceclient.Client` | `remoteAwareLabelService` | `gateway/resource_services.go:113` |
| 17 | chaossystem | `chaossystem.HandlerService` | `*resourceclient.Client` | `remoteAwareChaosSystemService` | `gateway/resource_services.go:170` |
| 18 | metric | `metric.HandlerService` | `*orchestratorclient.Client` + `*resourceclient.Client` (two-arg decorate) | `remoteAwareMetricService` | `gateway/metric_services.go:24` |
| 19 | system | `system.HandlerService` | `*systemclient.Client` | `remoteAwareSystemService` | `gateway/system_services.go:12` |
| 20 | systemmetric | `systemmetric.HandlerService` | `*systemclient.Client` | `remoteAwareSystemMetricService` | `gateway/system_services.go:80` |

(Total = 20 decorates; options.go block starts at line 57 and ends at 177.)

Pattern: every `remoteAwareX` embeds the local `HandlerService` and delegates only the remote-reachable methods to the gRPC client. On missing client config, returns `missingRemoteDependency(name)` (`gateway/remote_required.go:5`).

---

## 6. RequireConfiguredTargets inventory

All `RequiredConfigTarget` declarations found:

| Component (caller) | Name | PrimaryKey | LegacyKey | file:line |
|---|---|---|---|---|
| api-gateway | `iam-service` | `clients.iam.target` | `iam.grpc.target` | `gateway/options.go:48` |
| api-gateway | `orchestrator-service` | `clients.orchestrator.target` | `orchestrator.grpc.target` | `gateway/options.go:49` |
| api-gateway | `resource-service` | `clients.resource.target` | `resource.grpc.target` | `gateway/options.go:50` |
| api-gateway | `system-service` | `clients.system.target` | `system.grpc.target` | `gateway/options.go:51` |
| iam-service | `resource-service` | `clients.resource.target` | `resource.grpc.target` | `iam/options.go:24` |
| resource-service | `orchestrator-service` | `clients.orchestrator.target` | `orchestrator.grpc.target` | `resource/options.go:25` |
| runtime-worker-service | `orchestrator-service` | `clients.orchestrator.target` | `orchestrator.grpc.target` | `runtime/options.go:22` |
| system-service | `runtime-worker-service` | `clients.runtime.target` | `runtime_worker.grpc.target` | `system/options.go:24` |

Declaration location: `app/remote_require.go:13-17`. Validation is an `OnStart` lifecycle hook that errors if neither key is set.

`orchestrator-service` declares **no** required targets — it is a pure leaf.

---

## 7. Service-to-service dependency graph

Derived from §5 + §6:

```
api-gateway                → iam-service, orchestrator-service, resource-service, system-service
iam-service                → resource-service
orchestrator-service       → (none)
resource-service           → orchestrator-service
runtime-worker-service     → orchestrator-service
system-service             → runtime-worker-service
```

Adjacency list (caller → callee):
- `api-gateway`: iam, orchestrator, resource, system
- `iam-service`: resource (for `team.RemoteProjectReaderOption` project lookup)
- `resource-service`: orchestrator (for `evaluation.RemoteQueryOption` execution queries + `project.RemoteStatisticsOption`)
- `runtime-worker-service`: orchestrator (for `consumer.RemoteOwnerOptions` execution/injection owner RPC)
- `system-service`: runtime (for `system.RemoteRuntimeQueryOption` runtime status)
- `orchestrator-service`: — (sink; consumed by gateway/resource/runtime)

Notable: the graph is a DAG with orchestrator as sink, resource depends on orchestrator but orchestrator does NOT depend on resource (back-edge would be `resource ← orchestrator` via direct module fields). `runtime-worker-service` replaces the legacy consumer-in-process owner with a gRPC call to orchestrator (`service/consumer/owner_adapter.go:165`).

---

## 8. go.mod new/removed direct deps (`src/go.mod:7-63`)

Refactor-era direct requires clearly tied to the new architecture:

- `go.uber.org/fx v1.24.0` — fx DI kernel (go.mod:48)
- `google.golang.org/grpc v1.75.0` — gRPC transport for internalclients / grpc servers (go.mod:50)
- `google.golang.org/protobuf v1.36.8` — proto types for resource/runtime/system/orchestrator/iam v1 (go.mod:51)
- `go.etcd.io/etcd/client/v3 v3.6.7` — etcd coordination layer (go.mod:42) — `infra/etcd` module
- `github.com/ClickHouse/clickhouse-go/v2 v2.34.0` — telemetry DB (go.mod:9)
- `github.com/spf13/cobra v1.10.1` + `viper v1.19.0` — multi-mode CLI dispatch (go.mod:36-37)
- `sigs.k8s.io/controller-runtime v0.21.0` — controller module in runtime stack (go.mod:61)
- `github.com/prometheus/client_golang v1.22.0` — metrics (go.mod:31)
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.35.0` — OTLP exporter for tracing module (go.mod:44)

Replace directives in go.mod:312-322 pin chaos-experiment to local dir and various otlp/log modules, unchanged from pre-refactor intent.

---

## 9. Legacy vs refactored startup paths

**Legacy modes (`producer` / `consumer` / `both`) still exist but now run inside fx:**

- `producer` boot path: `main.go:68` → `fx.New(app.ProducerOptions(...))` → `ProducerHTTPOptions` adds `fx.Provide(newProducerInitializer)` and `fx.Invoke(registerProducerInitialization)` (`app/producer.go:30-31`). The `registerProducerInitialization` function (`app/producer.go:60-66`) attaches an `fx.Hook{OnStart}` that calls `initializer.start(ctx)`, which delegates to the legacy `initialization.InitializeProducer(db, redis, commonservice.NewConfigUpdateListener(...))` (`app/producer.go:49-58`) and then `utils.InitValidator()`.
- `consumer` boot path: `main.go:71` → `fx.New(app.ConsumerOptions(...))` → `CommonOptions + RuntimeWorkerStackOptions + ExecutionInjectionOwnerModules`. All lifecycle is now fx-managed via `worker.Module`, `controller.Module`, `receiver.Module` from `runtime_stack.go:32-36`; no monolithic `service/consumer.Run(...)` call.
- `both` boot path: `main.go:74` → `BothOptions` = `CommonOptions + RuntimeWorkerStackOptions + ProducerHTTPOptions`. Gives a single-process HTTP+consumer stack, equivalent to the old `both` mode but wired via fx.

**Bridge mechanism (legacy → fx):** `ProducerInitializer` struct (`app/producer.go:38-43`) holds `etcd`, `redis`, `db` refs and a `StartFunc` override used only in tests (`startup_smoke_test.go:78-82` / `service_entrypoints_test.go:94`). In production, `start()` executes the legacy `initialization.InitializeProducer` to set up config-update watchers + data-layer priming, then registers the gin validator. This is the single remaining bit of "legacy code called during startup" — it is not a full monolith anymore, just a setup hook.

**What has been replaced:**
- Old flat `service/producer` + `service/consumer` startup is gone from `main.go`; no direct calls to these packages there.
- Consumer workers are now Modules (`worker`, `controller`, `receiver`) with fx `OnStart` / `OnStop` lifecycle hooks (see spies in `startup_smoke_test.go:84-122`).
- Rate limiters and fault batch manager are now fx-Provided singletons (`runtime_stack.go:23-31`) instead of globals.
- gRPC servers for each microservice are their own fx Modules (`grpciam`, `grpcorchestrator`, `grpcresource`, `grpcruntime`, `grpcsystem`).

**What is still legacy:**
- `initialization.InitializeProducer` (`service/initialization`) is invoked inside the fx hook. Not refactored into fx-provided constructors yet.
- `commonservice.NewConfigUpdateListener` is created imperatively inside `start()` (`app/producer.go:53`) rather than via fx.
- `utils.InitValidator()` remains a global side-effect.
- `producer`, `consumer`, `both` cobra commands are kept for backward-compat; real deployments are expected to use the 6 dedicated microservice commands.

---

## 10. Surprises

1. **`both` relies on transitive `ExecutionInjectionOwnerModules`.** `BothOptions` doesn't explicitly include it, but `ProducerHTTPModules()` pulls it in via `ExecutionInjectionOwnerModules()` (`app/http_modules.go:45`). The `consumer` mode does include it explicitly at `app/consumer.go:9`. Inconsistent style.
2. **Orchestrator-service is a pure sink** — declares zero `RequiredConfigTarget`s. All other microservices depend on at least one peer, but orchestrator is only depended on. Surprising given its name.
3. **Legacy `both` mode = fx-powered monolith.** Running `rcabench both` gives you the entire app in one fx graph (Producer HTTP + all 7 runtime worker modules). This is still the `make local-debug` path per CLAUDE.md.
4. **Gateway is an HTTP shell that also wires infra.** `gateway.Options` includes `DataOptions` (db+redis), `CoordinationOptions` (etcd), `BuildInfraOptions` (harbor+helm+buildkit), `chaos.Module`, `k8s.Module`. An api-gateway with direct Kubernetes/buildkit/helm access is heavier than typical edge-gateway. Could be leftover from the producer refactor — the gateway runs the full `ProducerHTTPOptions` including the `ProducerInitializer` legacy startup hook.
5. **`resource-service` and `system-service` do NOT wire `chaos.Module` or `k8s.Module`,** but `system-service` does wire `k8s.Module` (`system/options.go:27`). Asymmetric: system needs k8s but gateway/iam/resource/orchestrator don't touch k8s directly in their graphs. Runtime-worker gets it via `RuntimeWorkerStackOptions`.
6. **`iam-service` wires `resourceclient.Module` + `team.RemoteProjectReaderOption()`** — the iam-service depends on resource-service purely to resolve which projects a team owns. This creates a `iam→resource→orchestrator` 3-hop chain that the gateway→iam→resource path traverses on team/project queries.
7. **`metric` decorator takes two remote clients** (`gateway/options.go:159-165`). It is the only decorate that fans out to both orchestrator AND resource; the implementation (`gateway/metric_services.go:44-81`) combines algorithm containers from resource with execution metrics from orchestrator client-side. All other decorates are single-client.
8. **`ProducerHTTPModules` wires 21 domain modules; gateway overrides 17 of them.** The 4 survivors never routed to a microservice: `pedestal`, `ratelimiter`, `sdk`, `router`. These are local-only at the gateway. Notably, the gateway *re-decorates* `middleware.Service` (produced inside `router.Module`) via a separate decorate call — the middleware auth chain is forced to use `iamclient` for token verification.
