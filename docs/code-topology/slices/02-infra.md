# infra/ gateway slice

> Historical note: parts of this document were written before the phase-2 gRPC collapse. Cross-check process-level claims against `docs/code-topology/README.md`, `docs/code-topology/slices/01-app-wiring.md`, and `docs/code-topology/slices/06-grpc-interfaces.md` first.

Paths (all absolute under `/home/ddq/AoyangSpace/aegis/AegisLab/src/`):
- `infra/{buildkit,chaos,config,db,etcd,harbor,helm,k8s,logger,loki,redis,runtime,tracing}/`

## 1. Per-package summary

| package | purpose | fx.Module contents | produced type | config keys read |
|---|---|---|---|---|
| buildkit | Wraps `moby/buildkit` client for image build; thin address helper. | `Provide(NewGateway)` (`infra/buildkit/module.go:5`) | `*buildkit.Gateway` (`infra/buildkit/gateway.go:14`) | `buildkit.address` |
| chaos | Initializes chaos-experiment singleton k8s client from `*rest.Config`. | `Invoke(Initialize)` (`infra/chaos/module.go:9`) | none (side effect) | — (consumes `*rest.Config`) |
| config | Boots viper via `aegis/config.Init(path)` from an `fx.Supply`'d `Params`. | `Invoke(Init)` (`infra/config/module.go:13`) | none (side effect, loads viper globals) | reads `ENV` env, file `config.<env>.toml` |
| db | Opens MySQL via GORM (3-retry), runs AutoMigrate + createDetectorViews, registers OTel GORM plugin. | `Provide(NewGormDB)` (`infra/db/module.go:17`) + `OnStop` close | `*gorm.DB` (`infra/db/module.go:21`) | `database.mysql.{host,port,user,password,db,timezone}` |
| etcd | Wraps `clientv3.Client`; Put/Get/Watch/Delete with optional TTL leases. | `Provide(NewGatewayWithLifecycle)` (`infra/etcd/module.go:5`) + `OnStop` close | `*etcd.Gateway` (`infra/etcd/gateway.go:15`) | `etcd.endpoints`, `etcd.username`, `etcd.password` |
| harbor | Harbor artifact registry queries (latest tag lookup, tag existence). | `Provide(NewGateway)` (`infra/harbor/module.go:5`) | `*harbor.Gateway` (`infra/harbor/gateway.go:17`) | `harbor.namespace`, `harbor.registry`, `harbor.username`, `harbor.password` |
| helm | Helm v3 action wrapper (AddRepo, UpdateRepo, Install+Uninstall of releases, cached chart lookup). | `Provide(NewGateway)` (`infra/helm/module.go:5`) | `*helm.Gateway` (`infra/helm/gateway.go:25`) | `helm.debug`, env `HELM_DRIVER` |
| k8s | Controller-runtime–free controller: dynamic informers for chaos CRDs, Jobs, Pods + workqueue; Gateway wraps Job CRUD, log fetch, VolumeMount parsing. | `Provide(ProvideController, NewGateway, ProvideRestConfig)` (`infra/k8s/module.go:9`) | `*k8s.Controller`, `*k8s.Gateway`, `*rest.Config` | `k8s.namespace`, `k8s.job.volume_mount.*`, `debugging.enabled` |
| logger | One-shot logrus formatter setup (nested formatter, InfoLevel, caller report). | `Invoke(Configure)` (`infra/logger/module.go:17`) | none | — |
| loki | HTTP client calling `/loki/api/v1/query_range` filtered by `task_id`. | `Provide(NewClient)` (`infra/loki/module.go:5`) | `*loki.Client` (`infra/loki/client.go:19`) | `loki.address`, `loki.timeout`, `loki.max_entries` |
| redis | `go-redis/v9` wrapper with hash/list/zset/set/stream/pubsub ops + task-queue helpers. | `Provide(NewGatewayWithLifecycle)` (`infra/redis/module.go:5`) + `OnStop` close | `*redis.Gateway` (`infra/redis/gateway.go:17`) | `redis.host` |
| runtime | Stamps process start time + ULID app-id into `consts.InitialTime` / `consts.AppID`. | `Invoke(InitializeRuntime)` (`infra/runtime/module.go:12`); note pkg name `runtimeinfra` | none (writes `consts`) | — |
| tracing | OTLP-HTTP trace exporter → OTel TracerProvider; sets global tracer + TextMapPropagator. | `Provide(NewTraceProvider)` (`infra/tracing/module.go:10`) + `OnStop` shutdown | `*sdktrace.TracerProvider` | `jaeger.endpoint`, `name`, `version` |

Composition sits in `app/app.go` (`BaseOptions`, `ObserveOptions`, `DataOptions`, `CoordinationOptions`, `BuildInfraOptions`, `CommonOptions`); `runtime`, `chaos`, `k8s` are attached in `app/runtime_stack.go:17` and `app/producer.go:20`.

## 2. Per-package exported API

### infra/buildkit (`infra/buildkit/gateway.go`)
- type `Gateway struct{}` (:14)
- `NewGateway() *Gateway` (:16)
- `(*Gateway).Address() string` (:20) reads `buildkit.address`
- `(*Gateway).Endpoint() string` (:24) → `tcp://<addr>`
- `(*Gateway).NewClient(ctx) (*buildkitclient.Client, error)` (:32)
- `(*Gateway).CheckHealth(ctx, timeout) error` (:40) TCP dial probe

### infra/chaos (`infra/chaos/module.go`)
- `Module` (fx.Module)
- `Initialize(restConfig *rest.Config)` (:13) → `chaosCli.InitWithConfig(restConfig)` on `github.com/OperationsPAI/chaos-experiment/client`. No other exports.

### infra/config (`infra/config/module.go`)
- `type Params struct{ Path string }` (:9)
- `Module` (fx.Module)
- `Init(params Params)` (:17) → delegates to top-level `aegis/config.Init(path)`.

### infra/db (`infra/db/{module,config,migration}.go`)
- `Module`
- `NewGormDB(lc fx.Lifecycle) *gorm.DB` (`module.go:21`)
- `type DatabaseConfig` with fields `Type,Host,Port,User,Password,Database,Timezone` (`config.go:9`)
- `NewDatabaseConfig(databaseType string) *DatabaseConfig` (`config.go:19`)
- `(*DatabaseConfig).ToDSN() (string, error)` (`config.go:31`) — only `"mysql"` supported
- unexported: `connectWithRetry`, `migrate`, `addDetectorJoins`, `createDetectorViews`

### infra/etcd (`infra/etcd/gateway.go`)
- `Module`
- `type Gateway struct{ client *clientv3.Client }` (:15)
- `NewGateway(client *clientv3.Client) *Gateway` (:19)
- `NewGatewayWithLifecycle(lc fx.Lifecycle) *Gateway` (:26)
- Methods: `Put(ctx,key,value,ttl)` (:39), `Get(ctx,key)` (:59), `Delete(ctx,key)` (:70), `Watch(ctx,key,withPrefix) WatchChan` (:77)

### infra/harbor (`infra/harbor/gateway.go`)
- `Module`
- `type Gateway struct{ namespace string; clientSet *harbor.ClientSet }` (:17)
- `NewGateway() *Gateway` (:22)
- `(*Gateway).GetLatestTag(image string) (string, error)` (:30)
- `(*Gateway).CheckImageExists(repository, tag string) (bool, error)` (:67)

### infra/helm (`infra/helm/gateway.go`)
- `Module`
- `type Gateway struct{}` (:25)
- `NewGateway() *Gateway` (:27)
- `(*Gateway).AddRepo(namespace, name, url string) error` (:31)
- `(*Gateway).UpdateRepo(namespace, name string) error` (:104)
- `(*Gateway).Install(ctx, namespace, releaseName, chartName, version, values, installTimeout, uninstallTimeout) error` (:82)
  (Install reuses tracing via `aegis/tracing.WithSpan` — `infra/helm/gateway.go:13`.)

### infra/k8s (`infra/k8s/{gateway,controller,crd,job}.go`)
- `Module` (providers: `ProvideController`, `NewGateway`, `ProvideRestConfig`)
- Gateway (`gateway.go:21`):
  - `NewGateway(controller *Controller) *Gateway` (:37)
  - `GetVolumeMountConfigMap()` (:44), `CreateJob(ctx,*JobConfig)` (:48), `GetJob(ctx,ns,name)` (:52), `WaitForJobCompletion(ctx,ns,name)` (:56), `GetJobPodLogs(ctx,ns,name)` (:60), `DeleteJob(ctx,ns,name)` (:64), `CheckHealth(ctx)` (:68)
- Controller (`controller.go:66`):
  - `newController() *Controller` (unexported, via `ProvideController`) (:80)
  - `(*Controller).Initialize(ctx, cancelFunc, cb Callback)` (:117)
  - `(*Controller).AddNamespaceInformers(namespaces []string) error` (:138)
  - `(*Controller).RemoveNamespaceInformers(namespaces []string)` (:238) — marks inactive; real informers keep running
- `type Callback interface` (:46): `HandleCRDAdd`, `HandleCRDDelete`, `HandleCRDFailed`, `HandleCRDSucceeded`, `HandleJobAdd`, `HandleJobFailed`, `HandleJobSucceeded` (7 methods)
- `type ActionType string` with consts `CheckRecovery`, `DeleteCRD`, `DeleteJob` (:36)
- `type QueueItem` (:56) used in retry workqueue
- `type VolumeMountConfig` (`job.go:24`) + `GetVolumeMount()` / `GetVolume()` methods
- `type JobConfig` (`job.go:41`) used by `CreateJob`
- Package-level sync.Once singletons: `k8sRestConfig`, `k8sClient`, `k8sDynamicClient`, `k8sController` (`gateway.go:26–35`)
- Factory accessors exported via `ProvideController()` / `ProvideRestConfig()` (`module.go:15,19`); `getK8sClient` / `getK8sDynamicClient` remain unexported

### infra/logger (`infra/logger/module.go`)
- `Module`, `Configure()` (:22) — sync.Once protects re-invocation

### infra/loki (`infra/loki/client.go`)
- `Module`
- `type Client struct{ address string; httpClient *http.Client }` (:19)
- `type QueryOpts struct{ Start,End time.Time; Limit int; Direction string }` (:24)
- `NewClient() *Client` (:42)
- `(*Client).QueryJobLogs(ctx, taskID, opts) ([]dto.LogEntry, error)` (:60) — LogQL `{app="rcabench"} | task_id=%q`. No health probe method.

### infra/redis (`infra/redis/{gateway,task_queue}.go`)
- `Module`, `NewGateway(client) *Gateway` (:21), `NewGatewayWithLifecycle(lc) *Gateway` (:28)
- `type Gateway struct{ client *redis.Client }` (:17)
- Generic ops (`gateway.go`, >40 methods, abbreviated): `CheckCachedField`, `GetHashField`, `SetHashField`, `ListRange`, `ListLength`, `ScanKeys`, `DeleteKey`, `SetMembers`, `Exists`, `HashGetAll`, `HashGet`, `HashSet`, `SeedNamespaceState`, `ZRangeByScoreWithScores`, `ZRangeByScore`, `SortedSetCard`, `ZAdd`, `ZRemRangeByScore`, `SetRemove`, `SetCard`, `XAdd`, `RunScript`, `Ping`, `HashLength`, `GetInt64`, `Watch`, `XRead`, `Publish`, `Set`, `SetNX`, `Subscribe`, `InitConcurrencyLock`
- Task-queue (`task_queue.go`): `SubmitImmediateTask`, `GetTask`, `HandleFailedTask`, `SubmitDelayedTask`, `ExpediteDelayedTask`, `ProcessDelayedTasks` (Lua script), `HandleCronRescheduleFailure`, `AcquireConcurrencyLock`, `ReleaseConcurrencyLock`, `GetTaskQueue`, `ListDelayedTasks`, `ListDeadLetterTasks`, `ListReadyTasks`, `RemoveFromList` (Lua), `RemoveFromZSet`, `DeleteTaskIndex`, `GetTaskQueueStats`
- Constants: `DelayedQueueKey="task:delayed"`, `ReadyQueueKey="task:ready"`, `DeadLetterKey="task:dead"`, `TaskIndexKey="task:index"`, `ConcurrencyLockKey="task:concurrency_lock"`, `MaxConcurrency=20` (:16–22)
- `type TaskQueueStats` (:25)

### infra/runtime (`infra/runtime/module.go`)
- package declared as `runtimeinfra` (:1) to avoid stdlib collision
- `Module`, `InitializeRuntime()` (:16) — populates `consts.InitialTime` and `consts.AppID` (ULID) from `aegis/utils`.

### infra/tracing (`infra/tracing/{module,provider}.go`)
- `Module`
- `NewTraceProvider(lc fx.Lifecycle) *sdktrace.TracerProvider` (`module.go:14`)
- `NewProvider() (*sdktrace.TracerProvider, error)` (`provider.go:18`) — OTLP HTTP insecure → `jaeger.endpoint`
- `ShutdownProvider(ctx, provider)` (`provider.go:50`)
- NOTE: `WithSpan`/`WithSpanNamed`/`WithSpanReturnValue`/`SetSpanAttribute` live in top-level `aegis/tracing` (`src/tracing/decorator.go:15–56`), not in `infra/tracing`.

## 3. fx lifecycle hooks

Every infra package that registers lifecycle hooks:

| pkg | OnStart | OnStop | code |
|---|---|---|---|
| infra/db | none (migrate+view creation runs at provider call time — `NewGormDB`, not in hook) | close underlying `*sql.DB` | `infra/db/module.go:25` |
| infra/etcd | none | `client.Close()` | `infra/etcd/gateway.go:29` |
| infra/redis | none | `client.Close()` | `infra/redis/gateway.go:31` |
| infra/tracing | none | `ShutdownProvider(ctx, provider)` (5s timeout) | `infra/tracing/module.go:20` |
| infra/config, logger, chaos, runtime | `fx.Invoke` only (run at graph build) | — | `Init`/`Configure`/`Initialize`/`InitializeRuntime` are pure start-time side effects |
| infra/buildkit, harbor, helm, loki, k8s | no hooks | no hooks | providers are lazy |

What they do:

- **etcd connection + watch init** — `newClient()` (`infra/etcd/gateway.go:99`) dials + `client.Status()` during construction; Watch is *on-demand* via `(*Gateway).Watch(...)`; no startup-time watch registration.
- **k8s controller informer start** — `*k8s.Controller.Initialize` is NOT called by the infra/k8s module itself. It is wired in `interface/controller/module.go:52` inside an `fx.Hook.OnStart`, spawning `go controller.Initialize(...)`. That function starts Job & Pod informers and the workqueue, then blocks on `<-ctx.Done()`; informers for chaos CRDs are added on demand via `AddNamespaceInformers` (called by module/injection flows).
- **redis singleton init** — `newClient()` (`infra/redis/gateway.go:363`) Pings on construction. Concurrency-lock seeding lives *outside* infra: `interface/worker/module.go:71` calls `params.RedisGateway.InitConcurrencyLock(ctx)` inside its own OnStart.
- **db Init + AutoMigrate + createDetectorViews** — all still present, executed synchronously in `NewGormDB` (`infra/db/module.go:22–23` → `connectWithRetry` → `migrate(db)` → `createDetectorViews(db)`), not inside a Lifecycle hook.
- **tracing OTLP exporter** — OTLP-HTTP with `WithInsecure()` + `WithEndpoint(jaeger.endpoint)`, `WithBatcher`; sets global `otel.SetTracerProvider` + composite propagator (TraceContext + Baggage). Shutdown via OnStop.
- **loki health probe** — none. `Client` has no `Ping`/`Health` method.

## 4. Consumers (inbound edges)

Grep of `aegis/infra/<pkg>` imports across `src/`:

| infra pkg | imported by |
|---|---|
| redis | `app` (app.go, producer.go, startup_smoke_test.go, service_entrypoints_test.go); `service/consumer/*` (task, trace, k8s_handler, monitor, redis, rate_limiter, restart_pedestal, algo_execution, build_container, collect_result, namespace_{catalog,lock,status}_store, rate_limiter_store, runtime_deps, runtime_snapshot); `service/common` (task, injection); `service/initialization` (consumer, producer); `module/{auth,container,execution,group,injection,notification,ratelimiter,system,systemmetric,task,trace}`; `interface/{receiver,worker,controller,grpc/runtime,grpc/orchestrator}` |
| k8s | `app` (producer, runtime_stack, system/options, gateway/options, smoke + entrypoint tests); `service/consumer` (algo_execution, build_datapack, common, config_handlers, k8s_handler, runtime_deps, runtime_snapshot); `service/initialization/consumer`; `module/system`; `interface/{controller,worker,grpc/runtime}` |
| db | `service/consumer/build_datapack`; `app/app` — most DB consumers depend on `*gorm.DB` directly (wide fan-out) |
| etcd | `app` (app, producer, smoke + entrypoint tests); `service/common/config_listener`; `module/system`; `interface/worker` |
| harbor | `app` (app, smoke + entrypoint tests) — only wiring; no business code imports directly (harbor logic accessed via service/common helpers that likely take `*harbor.Gateway` as param) |
| helm | `app` (app, smoke + entrypoint tests); `service/consumer/{restart_pedestal,runtime_deps,runtime_snapshot}`; `module/system`; `interface/{worker,grpc/runtime}` |
| loki | `app` (app, smoke + entrypoint tests); `module/task/{loki_gateway,service_test}`; `module/injection/service` |
| buildkit | `app` (app, smoke + entrypoint tests); `module/system`; `service/consumer/{build_container,runtime_deps,runtime_snapshot}`; `interface/{worker,grpc/runtime}` |
| chaos | `app/{gateway/options,producer,runtime_stack}` — ONLY wiring; chaos business code uses `github.com/OperationsPAI/chaos-experiment/*` directly |
| config | `app/app` only (sole entry point — every other caller uses top-level `aegis/config`) |
| logger | `app/app` only |
| runtime | `app/runtime_stack` only |
| tracing | `app/app` only (all `WithSpan` users import top-level `aegis/tracing`) |

## 5. config/ package (`src/config/`)

`infra/config` is a 17-line shim; the real package is top-level `src/config/`. Exports (`config/config.go`):

- `Init(configPath string)` (:37) — viper bootstrap + `validate()` + `AutomaticEnv()`
- `Get(key) any` / `GetString` / `GetInt` / `GetBool` / `GetFloat64` / `GetStringSlice` / `GetIntSlice` / `GetMap` / `GetList` (:83–132)
- `SetViperValue(key, value string, valueType consts.ConfigValueType) error` (:135) — typed setter (string/bool/int/float/stringArray)
- **Still present runtime-mutable state** (atomic):
  - `var detectorName atomic.Value` (:17)
  - `GetDetectorName() string` (:21)
  - `SetDetectorName(name string)` (:31)
  - Only `atomic.*` usage in the entire infra subtree: 0; only in `config/config.go:17`.
- `config/chaos_system.go` — package-level singletons, NOT atomic but `sync.Once` + `sync.RWMutex`:
  - `chaosConfigDB *gorm.DB`, `SetChaosConfigDB(db)`, `getDBForChaosConfig()` (:17–27)
  - `const ConfigKeyChaosSystem = "injection.system"` (:30)
  - `type ChaosSystemConfig struct{ System chaos.SystemType; Count int; NsPattern string; ExtractPattern string }` (:33)
  - `GetChaosSystemConfigManager() *chaosSystemConfigManager` (:51) — singleton via `managerOnce`
  - Manager methods: `Get(system)`, `GetAll()`, `Reload(callback)`, internal `load()` (tries `systems` table first, then falls back to `GetMap(ConfigKeyChaosSystem)`)
  - `(*ChaosSystemConfig).ExtractNsNumber(namespace string) (int, error)` (:171)
  - `GetAllNamespaces() ([]string, error)` (:195)
  - `convertPatternToTemplate(pattern string) string` (:224)
- `validate()` (:181) mandates `name, version, port, workspace, database.mysql.*, redis.host, jaeger.endpoint, k8s.namespace, k8s.job.service_account.name`.

## 6. infra/runtime

File: `infra/runtime/module.go` (22 lines total)
- Go package identifier is `runtimeinfra` (to avoid `runtime` stdlib name clash)
- Exports: `Module`, `InitializeRuntime()`
- Behavior: if `consts.InitialTime == nil` sets it to `utils.TimePtr(time.Now())`; if `consts.AppID == ""` sets it to `utils.GenerateULID(consts.InitialTime)`.
- Used only by `app/runtime_stack.go:19`. Not a gateway — just a one-shot fingerprint stamper.

## 7. chaos/ integration

`infra/chaos/module.go` (16 lines) is a minimal bridge:
- imports `chaosCli "github.com/OperationsPAI/chaos-experiment/client"` and `k8s.io/client-go/rest`
- `fx.Invoke(Initialize)`: `Initialize(restConfig *rest.Config) { chaosCli.InitWithConfig(restConfig) }`
- No types exported; no methods.
- The only OTHER infra package that touches chaos-experiment is `infra/k8s/controller.go:11` which uses `chaosCli.GetCRDMapping()` to enumerate chaos GVRs for the dynamic informer.
- Chaos business logic (spec building, handler, guidedcli) is consumed directly from `github.com/OperationsPAI/chaos-experiment/{handler,pkg/guidedcli}` by modules outside `infra/`.

## 8. k8s/ details

- **NOT controller-runtime / manager based.** Hand-rolled with `k8s.io/client-go/tools/cache.SharedIndexInformer`, `dynamicinformer`, `informers.NewSharedInformerFactoryWithOptions`, and `workqueue.TypedRateLimitingQueue` (`infra/k8s/controller.go:20–27`).
- **GVKs / GVRs watched** (dynamic): all entries of `chaosCli.GetCRDMapping()`, fetched at `controller.go:102`. As of `chaos-experiment/client/kubernetes.go:235`, that is 7 GVRs, all group `chaos-mesh.org`, version `v1alpha1`: `dnschaos, httpchaos, jvmchaos, networkchaos, podchaos, stresschaos, timechaos`.
- **Native resources watched**: `batch/v1 Jobs` and `core/v1 Pods` (`controller.go:99–100`) — filtered by `labelSelector=app-id=<consts.AppID>` (`controller.go:84`), scoped to `config.GetString("k8s.namespace")`.
- **Callback interface** still present — `controller.go:46–54`, 7 methods:
  ```
  HandleCRDAdd(name, annotations, labels)
  HandleCRDDelete(namespace, annotations, labels)
  HandleCRDFailed(name, annotations, labels, errMsg)
  HandleCRDSucceeded(namespace, pod, name, startTime, endTime, annotations, labels)
  HandleJobAdd(name, annotations, labels)
  HandleJobFailed(job *batchv1.Job, annotations, labels)
  HandleJobSucceeded(job *batchv1.Job, annotations, labels)
  ```
  The concrete implementer is `consumer.Handler` built in `interface/controller/module.go:55`.
- **Informer lifecycle**:
  1. At `fx` graph build, `ProvideController()` → `newController()` constructs informers but does NOT start them.
  2. `interface/controller/module.go` OnStart hook spawns `go controller.Initialize(ctx, cancel, handler)`.
  3. `Initialize` (`controller.go:117`) calls `startJobAndPodInformers()` — registers event handlers then `go informer.Run(ctx.Done())` for Job + Pod informers, waits for cache sync.
  4. Spawns `go startQueueWorker()` processing `QueueItem`s with ActionTypes `CheckRecovery / DeleteCRD / DeleteJob`.
  5. Blocks on `<-ctx.Done()` then `stop()` cancels ctx and `queue.ShutDown()` (`controller.go:266`).
  6. CRD informers added/removed per namespace via `AddNamespaceInformers` (`:138`) / `RemoveNamespaceInformers` (`:238`). Note: `RemoveNamespaceInformers` does NOT stop informers — it flips `activeNamespaces[ns]=false`, and event handlers gate on `isNamespaceActive` (`:710`).
- **Job event handler** (`controller.go:464`): on transition to backoff-exceeded calls `HandleJobFailed`; on first Succeeded calls `HandleJobSucceeded` and unless `debugging.enabled=true` enqueues a `DeleteJob` workqueue item.
- **Pod event handler** (`controller.go:515`): detects `ImagePullBackOff` (Waiting.Reason or Terminated.Reason) on Job-owned pods and triggers `HandleJobFailed` + delete-job queuing.
- `k8s_test.go` exists next to this package — left unread.

## 9. tracing/

- `infra/tracing` is a *provider module*, not a decorator helper package. Its only export beyond `Module` is `NewTraceProvider(lc)` / `NewProvider()` / `ShutdownProvider(ctx, provider)` (`provider.go:18,50`; `module.go:14`).
- Imports: `go.opentelemetry.io/otel`, `.../exporters/otlp/otlptrace/otlptracehttp`, `.../propagation`, `.../sdk/resource`, `.../sdk/trace`, `semconv/v1.34.0`, plus `aegis/config`, `logrus`.
- Exported side effects: `otel.SetTracerProvider(provider)` + `otel.SetTextMapPropagator(composite(TraceContext, Baggage))`.
- The decorator helpers live at the top-level package `aegis/tracing` (`src/tracing/decorator.go`), not in `infra/tracing`: `WithSpan`, `WithSpanNamed`, `WithSpanReturnValue[T]`, `SetSpanAttribute`. These obtain the tracer via `otel.Tracer("rcabench/task")`, so they depend on `infra/tracing` having run but don't import it.
- `infra/helm/gateway.go:13`, `infra/k8s/job.go:13` import `aegis/tracing` (top-level) to wrap install + createJob spans.

## 10. Surprises

1. **`Controller.Initialize` is spawned from `interface/controller`, not `infra/k8s`.** The infra module only provides the constructed controller — it does not start informers. If a run-mode wires `k8s.Module` without `controller.Module`, informers silently never start.
2. **`infra/config` is a two-line wrapper.** All runtime config state (viper singleton, atomic detector name, chaos-system singleton) still lives in top-level `aegis/config`. Every non-bootstrap caller imports `aegis/config`, so `infra/config.Module` is effectively write-once setup — removing `infra/config` would only break `app/app.go`.
3. **`atomic.Value` detector name persists** in `src/config/config.go:17`. `SetDetectorName` writes it at runtime; consumers call `GetDetectorName()` at hot-path time. The only `atomic.*` reference in the whole `config/`+`infra/` tree.
4. **`RemoveNamespaceInformers` leaks goroutines.** The comment at `infra/k8s/controller.go:236–237` admits informers cannot be gracefully stopped; the code just sets `activeNamespaces[ns]=false` and every event handler must remember to check `isNamespaceActive`. Any new handler forgetting the check will process stale events.
5. **`infra/k8s/gateway.go` keeps the full `sync.Once` singleton grid** (`k8sRestConfigOnce/k8sClientOnce/k8sDynamicClientOnce/controllerOnce`) even though `fx` already provides singleton semantics. Result: `getK8sClient()`/`getK8sDynamicClient()` are reachable from anywhere in the package without going through DI — the Gateway type is window-dressing over package-global state.
6. **`infra/chaos/module.go` passes `*rest.Config` from `ProvideRestConfig`** (`infra/k8s/module.go:19`) → means `chaos.Module` implicitly depends on `k8s.Module`. Nothing in code prevents wiring `chaos.Module` without `k8s.Module`; fx would fail at graph resolution.
7. **`infra/db.NewGormDB` runs `AutoMigrate` + `createDetectorViews` in the constructor, not a Lifecycle OnStart hook.** That means DB schema work happens during fx graph *build*, before `app.Start()`. If construction panics mid-migration the `OnStop` close hook has not yet been registered.
8. **Stale config keys / coupling**: `helm` gateway only reads `helm.debug` (everything else comes from env `HELM_DRIVER` or per-call args); `buildkit.address` alone governs both client and health probe; `loki.timeout` is parsed as a duration string (silently falls back to 10s on parse error — no warning). `jaeger.endpoint` is used by `tracing` but actually hits an OTLP-HTTP endpoint (legacy name kept).
