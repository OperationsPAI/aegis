# 05 — Orchestration slice

> Archival note: this file was not fully revalidated after the phase-2 gRPC collapse and phase-6 module-wiring cleanup. Treat `docs/code-topology/README.md`, `docs/code-topology/slices/01-app-wiring.md`, and `docs/code-topology/slices/06-grpc-interfaces.md` as the current topology source of truth.

Paths are relative to `/home/ddq/AoyangSpace/aegis/AegisLab/src`. All citations are file:line.

---

## 1. Per-module breakdown

### 1.1 `module/injection/`
Files: `module.go`, `handler.go`, `handler_service.go`, `service.go`, `repository.go`, `submit.go`, `api_types.go`, `archive.go`, `datapack_store.go`, `query_datapack_arrow.go`, `query_datapack_noarrow.go`, `resolve.go`, `runtime_types.go`, `spec_convert.go`, `time_range.go`.

`fx.Module` (`module.go:5-11`): provides `NewRepository`, `NewDatapackStore`, `NewService`, `AsHandlerService`, `NewHandler`.

`HandlerService` interface (`handler_service.go:13-34`): `ListProjectInjections`, `Search`, `ListNoIssues`, `ListWithIssues`, `SubmitFaultInjection`, `SubmitDatapackBuilding`, `ListInjections`, `GetInjection`, `ManageLabels`, `BatchManageLabels`, `BatchDelete`, `Clone`, `GetLogs`, `GetDatapackFilename`, `DownloadDatapack`, `GetDatapackFiles`, `DownloadDatapackFile`, `QueryDatapackFile`, `UpdateGroundtruth`, `UploadDatapack`.

Handler routes (`handler.go`): `GET /api/v2/projects/:pid/injections`, `POST /.../search`, `GET /.../analysis/no-issues`, `GET /.../analysis/with-issues`, `POST /.../inject`, `POST /.../build`, `GET /api/v2/injections/:id`, `GET /api/v2/injections/metadata` (**410 Gone**, `handler.go:286`), `GET /api/v2/injections/systems`, `POST /api/v2/injections/translate` (**410 Gone**, `handler.go:335`), `PATCH /api/v2/injections/:id/labels`, `PATCH /api/v2/injections/labels/batch`, `POST /api/v2/injections/batch-delete`, `POST /api/v2/injections/:id/clone`, `GET /api/v2/injections/:id/download`, `GET /api/v2/injections/:id/files`, `GET /api/v2/injections/:id/files/download`, `GET /api/v2/injections/:id/files/query`, `PUT /api/v2/injections/:id/groundtruth`, `POST /api/v2/injections/upload`.

`Service` struct (`service.go:27-36`): `repo *Repository`, `store *DatapackStore`, `lokiClient *loki.Client`, `redis *redis.Gateway`.

Edges:
- **Repo** via GORM (`model.FaultInjection`, `model.Project`, `model.Task`).
- **Infra**: `aegis/infra/loki`, `aegis/infra/redis`.
- **Cross-module**: `aegis/module/container` (`NewRepository(db).ResolveContainerVersions`, `ListHelmConfigValues`, `ListContainerVersionEnvVars` — service.go:169-212, 280-302), `aegis/module/label` (`CreateOrUpdateLabelsFromItems`).
- **service/common**: `common.SubmitTaskWithDB` (`service.go:341`, `service.go:430`).
- **Runtime-owner types** defined for adapter: `RuntimeCreateInjectionReq`, `RuntimeUpdateInjectionStateReq`, `RuntimeUpdateInjectionTimestampReq` (`runtime_types.go`) — consumed by `service/consumer/owner_adapter.go` and `interface/grpc/orchestrator`.
- **External**: `github.com/OperationsPAI/chaos-experiment/handler` and `.../pkg/guidedcli` (`submit.go:13-15`).

Notable: `submit.go` — batch resolving and dedup of guided vs legacy specs; `datapack_store.go` — JuiceFS/zip handling; `query_datapack_arrow.go` / `..._noarrow.go` — build-tagged Arrow-IPC support.

### 1.2 `module/execution/`
Files: `module.go`, `handler.go`, `handler_service.go`, `service.go`, `repository.go`, `api_types.go`, `result_types.go`, `runtime_types.go`.

`fx.Module` (`module.go:5-10`): `NewRepository`, `NewService`, `AsHandlerService`, `NewHandler`.

`HandlerService` (`handler_service.go:10-20`): `ListProjectExecutions`, `SubmitAlgorithmExecution`, `ListExecutions`, `GetExecution`, `ListAvailableLabels`, `ManageLabels`, `BatchDelete`, `UploadDetectorResults`, `UploadGranularityResults`.

Handler routes (`handler.go`): `GET /api/v2/projects/:pid/executions`, `POST /api/v2/projects/:pid/executions/execute`, `GET /api/v2/executions/:id`, `GET /api/v2/executions/labels`, `PATCH /api/v2/executions/:id/labels`, `POST /api/v2/executions/batch-delete`, `POST /api/v2/executions/:eid/detector_results`, `POST /api/v2/executions/:eid/granularity_results`.

`Service` (`service.go:23-30`): `repo *Repository`, `redis *redis.Gateway`.

Edges:
- **Repo**: `model.Execution`, `model.DetectorResult`, `model.GranularityResult`, `model.Project`.
- **Cross-module**: `aegis/module/container` (`service.go:74,100`), `aegis/module/injection.NewRepository(...).ResolveDatapacks` (`service.go:84`), `aegis/module/label`.
- **service/common**: `common.SubmitTaskWithDB` (`service.go:124`).
- **runtime_types.go**: `RuntimeCreateExecutionReq`, `RuntimeUpdateExecutionStateReq` (consumed by consumer owner adapter).

### 1.3 `module/task/`
Files: `module.go`, `handler.go`, `handler_service.go`, `service.go`, `repository.go`, `log_service.go`, `log_types.go`, `loki_gateway.go`, `queue_store.go`, `api_types.go`, `service_test.go`.

`fx.Module` (`module.go:5-13`): `NewRepository`, `NewTaskQueueStore`, `NewLokiGateway`, `NewTaskLogService`, `NewService`, `AsHandlerService`, `NewHandler`.

`HandlerService` (`handler_service.go:13-20`): `BatchDelete`, `GetDetail`, `List`, `GetForLogStream`, `StreamLogs`, `Expedite`.

Routes (`handler.go`): `POST /api/v2/tasks/batch-delete`, `GET /api/v2/tasks/:tid`, `POST /api/v2/tasks/:tid/expedite`, `GET /api/v2/tasks`, `GET /api/v2/tasks/:tid/logs/ws` (WebSocket log streaming).

`Service` (`service.go:19-33`): `repository *Repository`, `logService *TaskLogService`, `loki *LokiGateway`, `redis *redis.Gateway`.

`TaskLogService` (`log_service.go:27-39`): subscribes Redis Pub/Sub `joblogs:<taskID>`, queries Loki, polls DB for terminal state, flushes and closes.

`TaskQueueStore` (`queue_store.go`): thin wrapper exposing `SubscribeJobLogs` (channel `joblogs:<taskID>`, `queue_store.go:11,22`).

`Expedite` (`service.go:88-120`) updates DB `execute_time` and calls `redis.ExpediteDelayedTask`, emits `consts.EventTaskScheduled` to trace stream.

Cross-module: none — task module is leaf with respect to other slice modules. Model deps include `model.Task` with preloads on `FaultInjection`, `Execution` (`repository.go:32-45`).

### 1.4 `module/trace/`
Files: `module.go`, `handler.go`, `handler_service.go`, `service.go`, `repository.go`, `stream.go`, `api_types.go`.

`fx.Module` (`module.go:5-10`): `NewRepository`, `NewService`, `AsHandlerService`, `NewHandler`.

`HandlerService` (`handler_service.go:13-18`): `GetTrace`, `ListTraces`, `GetTraceStreamProcessor`, `ReadTraceStreamMessages`.

Routes (`handler.go`): `GET /api/v2/traces/:tid`, `GET /api/v2/traces` (list), `GET /api/v2/traces/:tid/stream` (SSE via `consts.StreamTraceLogKey` = `trace:%s:log`, `handler.go:146`).

`Service` (`service.go:16-23`): `repo *Repository`, `redis *redis.Gateway`. `GetTraceStreamAlgorithms` (`service.go:61`) reads `consts.InjectionAlgorithmsKey` hash by groupID to compute per-trace completion.

`stream.go` — `StreamProcessor` with `payloadTypeRegistry` (`stream.go:16-25`) mapping event types (`EventAlgoRunStarted`, `EventAlgoRunSucceed`, `EventDatapackBuildStarted`, ...) to payload Go types.

Cross-module: `aegis/config.GetDetectorName` (filter detector from algo list).

### 1.5 `module/group/`
Files: `module.go`, `handler.go`, `handler_service.go`, `service.go`, `repository.go`, `api_types.go`.

`fx.Module` (`module.go:5-10`): `NewRepository`, `NewService`, `AsHandlerService`, `NewHandler`.

`HandlerService` (`handler_service.go:11-15`): `GetGroupStats`, `NewGroupStreamProcessor`, `ReadGroupStreamMessages`.

Routes (`handler.go`): `GET /api/v2/groups/:gid/stats`, `GET /api/v2/groups/:gid/stream` (SSE via `consts.StreamGroupLogKey` = `group:%s:log`, `handler.go:110`).

`Service` (`service.go:16-23`): `repo *Repository`, `redis *redis.Gateway`. Aggregates trace durations / state histogram (`service.go:25-61`).

Cross-module: imported by `service/consumer/trace.go` (`import group "aegis/module/group"`).

### 1.6 `module/notification/`
Files: `module.go`, `handler.go`, `handler_service.go`, `service.go`, `repository.go`, `api_types.go`.

`fx.Module` (`module.go:5-10`): `NewRepository`, `NewService`, `AsHandlerService`, `NewHandler`.

`HandlerService` (`handler_service.go:11-13`): `ReadStreamMessages` (single method).

Route: `GET /api/v2/notifications/stream` (SSE via `consts.NotificationStreamKey` = `notifications:global`, `handler.go:60`).

`Service` (`service.go:13-20`): `repo *Repository`, `redis *redis.Gateway` — only reads the global stream; no DB work (repository.go is a 11-line stub).

---

## 2. Async engine current state (`service/consumer/`)

- `ConsumeTasks(ctx, deps RuntimeDeps)` (`task.go:219-246`) and `StartScheduler(ctx, gateway)` (`task.go:154-166`) are **still** the legacy entry points. They are now launched from `interface/worker/module.go:75-76` inside the fx `OnStart` lifecycle hook; the old `main.go go consumer.StartScheduler(...)` goroutines are gone.
- `processDelayedTasks` (`task.go:168-212`) promotes due tasks and re-enqueues cron tasks via `redisGateway.ProcessDelayedTasks` + `SubmitDelayedTask`.
- Dispatch table: `dispatchTask` (`distribute_tasks.go:15-56`) — see Section 11.

### Store / helper files
- `runtime_snapshot.go:42-175` — `RuntimeSnapshotService` gathers health pings (DB / Redis / K8s / BuildKit / Helm) + token-bucket usage. Used by `grpcruntime.service` status RPC.
- `runtime_deps.go:12-25` — `RuntimeDeps` struct bundling all gateways/limiters/owners that `dispatchTask` needs.
- `owner_adapter.go:29-79` — `ExecutionOwner` / `InjectionOwner` adapters that route to either `orchestratorclient.Client` (remote RPC) or the local `execution.Service` / `injection.Service` based on `orchestrator.Enabled()`. `RemoteOwnerOptions()` (`owner_adapter.go:166-171`) decorates to force remote-only in the dedicated runtime-worker-service build.
- `namespace_catalog_store.go` — Redis-set of namespaces keyed by `consts.NamespacesKey` (`monitor:namespaces`); per-namespace metadata at `consts.NamespaceKeyPattern` (`monitor:ns:%s`). One-line purpose: catalog of known namespaces.
- `namespace_lock_store.go` — atomic `WATCH`-based lock / release / isActive for namespace leases, stored at `monitor:ns:%s` hash fields `end_time` + `trace_id` (`namespace_lock_store.go:85-113`).
- `namespace_status_store.go` — get/set `status` hash field on same key (Enabled/Disabled/Deleted).
- `rate_limiter_store.go` — Redis-SET + Lua script token bucket keyed by `token_bucket:restart_service|build_container|algo_execution` (`rate_limiter_store.go:20-46`).
- `state_store.go` — thin struct wrapping `ExecutionOwner`/`InjectionOwner`; used by `k8s_handler.go` to update execution/injection state during CRD/Job callbacks.
- `redis.go` — helpers: `consumerDetachedContext()` (returns `context.TODO()`), `publishRedisStreamEvent`, `publishTraceStreamEvent`, `loadCachedInjectionAlgorithms` (reads `consts.InjectionAlgorithmsKey`).

### Factory wiring (traced from `app/runtime_stack.go:24-30`)
- `NewMonitor(gateway *redis.Gateway) NamespaceMonitor` (`monitor.go:66-74`) — builds `monitor{...}` with the three namespace stores.
- `NewFaultBatchManager()` (`fault_injection.go:45-50`) — in-memory map of batch progress for hybrid injections.
- `NewExecutionOwner(params)` / `NewInjectionOwner(params)` (`owner_adapter.go:49-63`) — DI-picks local vs remote.
- `NewRestartPedestalRateLimiter` / `NewBuildContainerRateLimiter` / `NewAlgoExecutionRateLimiter` (`rate_limiter.go:179-207`) — each reads `rate_limiting.max_concurrent_*` dynamic config and constructs a `TokenBucketRateLimiter` backed by `tokenBucketStore`.
- `NewMonitor` returns interface `NamespaceMonitor` (`monitor.go:47-54`): `SetContext`, `InitializeNamespaces`, `RefreshNamespaces`, `ReleaseLock`, `CheckNamespaceToInject`, `GetNamespaceToRestart`. `AcquireLock` is unexported on the monitor but still used internally by `CheckNamespaceToInject`/`GetNamespaceToRestart`.

---

## 3. `service/common/`

File-level summary (one line each):
- `task.go` — **canonical enqueue API**: `SubmitTaskWithDB(ctx, db, redisGateway, t *dto.UnifiedTask)` (`task.go:56-151`). Assigns IDs, computes parent level, upserts `model.Trace` + `model.Task`, pushes to Redis ready/delayed queue via `gateway.SubmitImmediateTask` / `SubmitDelayedTask`, emits `EventTaskScheduled`. Also exports `EmitTaskScheduled` (`task.go:155-172`) and `CronNextTime` (`task.go:27-35`). **Callers within slice**: `module/injection/service.go:341,430`, `module/execution/service.go:124`, `service/consumer/k8s_handler.go:327,630,706`, `service/consumer/restart_pedestal.go` via `common.ProduceFaultInjectionTasksWithDB`, `service/consumer/algo_execution.go:186` (reschedule), `service/consumer/restart_pedestal.go:231` (reschedule).
- `injection.go` — `ProduceFaultInjectionTasksWithDB(ctx, db, redis, task, injectTime, payload)` (`injection.go:15-35`) — wraps `SubmitTaskWithDB` for the restart→inject transition inside `executeRestartPedestal`.
- `config_listener.go` — `ConfigUpdateListener` + `NewConfigUpdateListener` (`config_listener.go:39-56`), `EnsureScope` (`config_listener.go:62-90`), watches etcd prefixes `ConfigEtcdProducerPrefix|ConsumerPrefix|GlobalPrefix`. Dispatches via `handleConfigChange`.
- `config_registry.go` — `ConfigHandler` interface + singleton `configRegistry` + `RegisterHandler`, `RegisterGlobalHandlers` (`config_registry.go:49-55` registers `algoConfigHandler` for `algo.detector`), `PublishWrapper` (publishes to Redis `consts.ConfigUpdateResponseChannel` = `config:updates:response`).
- `config_store.go` — `configStore{db}`; `getConfigByKey`, `listConfigsByScope` against `model.DynamicConfig`.
- `dynamic_config.go` — `ValidateConfig`, `ValidateConfigMetadataConstraints`, `CreateConfig` (used only by `initialization/`).
- `metadata_store.go` — `DBMetadataStore` implements `chaos.MetadataStore` (service endpoints, java class methods, network pairs, DB/gRPC ops). Plugged globally via `chaos.SetMetadataStore(...)` in `initialization/systems.go:63-64`.

---

## 4. `service/initialization/` and `service/logreceiver/`

**`service/initialization/`** (`consumer.go`, `producer.go`, `systems.go`, `common.go`, `bootstrap_store.go`, `types.go`, `utils.go`)

`InitializeProducer(db, publisher, listener)` (`producer.go:35-61`) — seeds the initial resources/roles/permissions/admin user/projects/teams/users/containers/datasets/execution labels in one transaction, then calls `InitializeSystems(db)` (seeds 6 builtin systems, `systems.go:16-23`; registers systems with `chaos-experiment`; sets `chaos.SetMetadataStore(common.NewDBMetadataStore(db))`). Finally calls `common.RegisterGlobalHandlers(publisher)` and `activateConfigScope(producerScope, listener)`. Invoked from `app/producer.go:53` inside `ProducerInitializer.start`.

`InitializeConsumer(ctx, db, controller, monitor, publisher, listener, restartLimiter, buildLimiter, algoLimiter)` (`consumer.go:21-93`) — same-shape bootstrap for consumer-scope dynamic-config rows; registers `common.RegisterGlobalHandlers` + `consumer.RegisterConsumerHandlers` (chaos system count + rate-limiting config); auto-GC of leaked rate-limiter tokens via `module/ratelimiter.NewService(publisher, db).GC(ctx)` (`consumer.go:54-60`); runs `monitor.InitializeNamespaces()` + `consumer.UpdateK8sController(controller, initialized, []string{})` in a background goroutine to avoid fx deadline (`consumer.go:72-90`). Invoked from `interface/worker/module.go:58-68`.

**`service/logreceiver/`** (`receiver.go`, `parser.go`)

`OTLPLogReceiver` (`receiver.go:37-48`) — OTLP-HTTP server (`/v1/logs`, `/health`) defaulting to port `4319` (`receiver.go:27`). `NewOTLPLogReceiver(port, maxRequestSize, publisher logPublisher)` takes a `logPublisher` interface (Publish only) — satisfied by `*redis.Gateway`. Incoming log records are parsed (`parser.go`) and published to Redis Pub/Sub channel `joblogs:<taskID>` (prefix const `PubSubChannelPrefix = "joblogs"`, `receiver.go:33`). Invoked from `interface/receiver/module.go:25-44`.

---

## 5. `interface/worker/module.go`

`fx.Module("worker", ...)` (`module.go:19-22`):
- `Provide(newLifecycle)`; `Invoke(registerLifecycle)`.
- `Params` (`module.go:24-41`) pulls DB, `redis.Gateway`, `buildkit.Gateway`, `helm.Gateway`, `k8s.Gateway`, `k8s.Controller`, `etcd.Gateway`, `consumer.NamespaceMonitor`, three named `TokenBucketRateLimiter`s (`name:"restart_limiter|build_limiter|algo_limiter"`), `FaultBatchManager`, `ExecutionOwner`, `InjectionOwner`.

`Lifecycle.start` (`module.go:53-91`):
1. `initialization.InitializeConsumer(ctx, ...)` with a freshly constructed `commonservice.NewConfigUpdateListener(ctx, db, etcd)`.
2. `params.RedisGateway.InitConcurrencyLock(ctx)`.
3. `go consumer.StartScheduler(ctx, params.RedisGateway)` (`module.go:75`).
4. `go consumer.ConsumeTasks(ctx, consumer.RuntimeDeps{...})` assembling `RuntimeDeps` from params (`module.go:76-89`).

`registerLifecycle` (`module.go:99-118`) adds an `fx.Hook` that creates a cancellable `workerCtx` and calls `start`; on stop it cancels. This is the fx-era replacement for the old `go consumer.StartScheduler(ctx); consumer.ConsumeTasks(ctx)` in `main.go`.

---

## 6. `interface/controller/module.go`

`fx.Module("controller", ...)` (`module.go:18-21`).

`Params` (`module.go:23-35`): `k8s.Controller`, `k8s.Gateway`, `redis.Gateway`, `*gorm.DB`, `consumer.NamespaceMonitor`, `algo_limiter` (named), `FaultBatchManager`, `ExecutionOwner`, `InjectionOwner`.

`Lifecycle.start` (`module.go:47-67`):
- Sets `k8slogger.SetLogger(stdr.New(...))`.
- `go r.params.Controller.Initialize(ctx, cancel, consumer.NewHandler(db, monitor, algoLimiter, k8sGateway, redisGateway, batchManager, execution, injection))` — the `consumer.NewHandler` (defined in `service/consumer/k8s_handler.go:139-149`) **is** the CRD-success callback object, implementing `HandleCRDAdd`, `HandleCRDDelete`, `HandleCRDFailed`, `HandleCRDSucceeded`, `HandleJobAdd`, `HandleJobFailed`, `HandleJobSucceeded`.

So yes — the callback is still `k8s.Controller.Initialize(ctx, cancel, callback)`, with the callback built from fx-provided deps rather than from global singletons.

---

## 7. `interface/receiver/module.go`

`fx.Module("receiver", ...)` (`module.go:14-17`).

`newLifecycle(redisGateway *redis.Gateway)` (`module.go:25-33`) reads port from dynamic config `otlp_receiver.port`, falls back to `logreceiver.DefaultPort=4319`, constructs `logreceiver.NewOTLPLogReceiver(port, 0, redisGateway)`.

`Lifecycle.start` (`module.go:35-45`) launches `go r.receiver.Start(ctx)`. `OnStop` calls `r.receiver.Shutdown()` (`module.go:47-55`). Replaces old `main.go go logreceiver.NewOTLPLogReceiver(port, 0).Start(ctx)`.

---

## 8. Full workflow chain

Starting from `POST /api/v2/projects/:pid/injections/inject`:

1. Gin router → `injection.Handler.SubmitProjectFaultInjection` (`module/injection/handler.go:202-209`) → `h.submitFaultInjection(c, &projectID)` (`handler.go:782`).
2. Middleware stash: `groupID = c.GetString("groupID")`, `userID = middleware.GetCurrentUserID(c)`; span extracted via `spanFromGin`; body bound to `SubmitInjectionReq`, validated, spec auto-detected via `req.ResolveSpecs(FriendlySpecToNode)` (`handler.go:809`).
3. `injection.Service.SubmitFaultInjection` (`module/injection/service.go:152-359`):
   - Resolves pedestal + benchmark container versions (`container.NewRepository(...).ResolveContainerVersions`).
   - Loads pedestal helm config + dynamic helm values.
   - Calls `parseBatchGuidedSpecs` (`submit.go:225-279`) or `parseBatchInjectionSpecs` (`submit.go:28-96`) per batch.
   - Dedups via `s.removeDuplicated` (`submit.go:123-192`) against Redis-cached engine configs and the DB.
   - If `req.Algorithms` non-empty, caches algorithm list at `redis.SetHashField(ctx, consts.InjectionAlgorithmsKey, groupID, items)` (`service.go:298`) — key `injection:algorithms`.
   - Builds a `dto.UnifiedTask{Type: consts.TaskTypeRestartPedestal, Immediate: false, ExecuteTime: executeTime, Payload: {...}, ...}` per unique batch. Sets `task.Extra[TaskExtraInjectionAlgorithms] = len(req.Algorithms)`.
   - `common.SubmitTaskWithDB(ctx, db, s.redis, task)` (`service.go:341`).
4. `common.SubmitTaskWithDB` (`service/common/task.go:56-151`):
   - Creates Trace + Task rows (upsert in one txn).
   - Because `Immediate == false`, pushes to Redis via `redisGateway.SubmitDelayedTask` → `ZADD task:delayed <executeTime> <json>` + `HSET task:index <taskID> task:delayed` (`infra/redis/task_queue.go:67-76`).
   - Emits `EventTaskScheduled` to `trace:<traceID>:log` stream (`task.go:155-172`).
5. **Scheduler promotion**: `consumer.StartScheduler` (`service/consumer/task.go:154-166`) ticks every 1s → `redisGateway.ProcessDelayedTasks(ctx)` (`infra/redis/task_queue.go:124`) which `ZRANGEBYSCORE`s due entries, `LPUSH`es them to `task:ready`, updates `task:index`.
6. **Worker consumption**: `consumer.ConsumeTasks` (`task.go:219-246`) `AcquireConcurrencyLock` (max `MaxConcurrency=20`, `task.go:43`), then `BRPOP task:ready 30s` via `redisGateway.GetTask` (`infra/redis/task_queue.go:41-48`). Spawns `go processTask` per item.
7. `processTask` (`task.go:249-279`) unmarshals `dto.UnifiedTask`, rebuilds OTel contexts via `extractContext` (`task.go:288-320`) and calls `executeTaskWithRetry` → `dispatchTask` (`distribute_tasks.go:15-56`).
8. For `TaskTypeRestartPedestal`: `executeRestartPedestal` (`service/consumer/restart_pedestal.go:33-199`):
   - Acquires restart token from `token_bucket:restart_service`.
   - Calls `monitor.GetNamespaceToRestart` — picks an enabled namespace matching the system `NsPattern`, atomically locks it at `monitor:ns:<ns>`.
   - `installPedestal` via helm.
   - Builds the inject payload adding `InjectNamespace`, `InjectPedestal`, `InjectPedestalID` and calls `common.ProduceFaultInjectionTasksWithDB` (`restart_pedestal.go:192`) which submits a **new** `TaskTypeFaultInjection` delayed-task keyed off `injectTime`.
9. Scheduler promotes inject task → worker picks up → `executeFaultInjection` (`fault_injection.go:100-282`):
   - `monitor.CheckNamespaceToInject` reacquires/validates the same namespace lock.
   - Decodes `payload.nodes` (legacy) **or** `payload.guidedConfigs` (new path) into `[]chaos.InjectionConf`, computes display/groundtruth.
   - Sets CRD annotations `TaskCarrier`, `TraceCarrier`, `CRDAnnotationBenchmark`; CRD labels include `K8sLabelAppID`, `CRDLabelBatchID`, `CRDLabelIsHybrid`, plus `task.GetLabels()` (taskID/traceID/groupID/projectID/userID/taskType).
   - `chaos.BatchCreate(ctx, injectionConfs, system, namespace, annotations, crdLabels)` — writes Chaos Mesh CRDs in parallel.
   - `deps.InjectionOwner.CreateInjection(...)` writes the `model.FaultInjection` row via local service or orchestrator RPC.
10. **K8s CRD succeeded**: `k8s.Controller` informer fires `k8sHandler.HandleCRDSucceeded` (`service/consumer/k8s_handler.go:256-347`):
   - Parses annotations + labels, rebuilds task/trace contexts from OTel carriers.
   - `updateTaskState(..., TaskCompleted, ..., EventFaultInjectionCompleted, ...)`.
   - `store.updateInjectionState(injectionName, DatapackInjectSuccess)`; `store.updateInjectionTimestamp(injectionName, startTime, endTime)`.
   - Submits **`TaskTypeBuildDatapack`** via `common.SubmitTaskWithDB` (`k8s_handler.go:327`) with `ParentTaskID = taskID`, same traceID/groupID/projectID/userID.
11. **BuildDatapack**: dispatched to `executeBuildDatapackWithDeps` (`build_datapack.go:54-98`) — creates a K8s Job via `k8sGateway.CreateJob` with labels/annotations carrying datapack identity.
12. `HandleJobAdd` publishes `EventDatapackBuildStarted`/`EventAlgoRunStarted` on `trace:<traceID>:log` (`k8s_handler.go:349-394`).
13. **Job succeeded**: `HandleJobSucceeded` (`k8s_handler.go:525-710`):
   - `TaskTypeBuildDatapack` branch: updates injection state `DatapackBuildSuccess`, marks task `TaskCompleted` with `EventDatapackBuildSucceed`, then **always** submits a `TaskTypeRunAlgorithm` task for the **detector** algorithm (`config.GetDetectorName()`), passing the datapack item.
   - `TaskTypeRunAlgorithm` branch: releases algo rate-limit token, updates execution state `ExecutionSuccess` (or `DatapackDetectorSuccess` if container was the detector), marks task complete with `EventAlgoRunSucceed`, then submits a `TaskTypeCollectResult` child task.
14. `executeAlgorithm` (`algo_execution.go:61-155`) runs the user's algorithm list — these were pre-cached at step 3 in `injection:algorithms`, popped by... the trace-stream processor (`module/trace/service.go:61-85`) and by `collect_result.go`. For each algorithm, `createAlgoJob` creates a K8s Job; `createExecution` writes `model.Execution` via `deps.ExecutionOwner.CreateExecution`.
15. `executeCollectResult` (`collect_result.go:27-`) fetches detector or granularity results via the local owner or gRPC, emits `EventDatapackResultCollection` / `EventAlgoResultCollection` to `trace:<traceID>:log`. Updates group stream at `group:<groupID>:log` when the trace completes (via `consumer/trace.go`).

Throughout, every state transition calls `updateTaskState` (`service/consumer/task.go:503-549`) which updates `model.Task.state` and publishes a `dto.TraceStreamEvent` to `trace:<traceID>:log`. `group:<groupID>:log` events come from `consumer/trace.go`. Global workflow notifications (`notifications:global`) are produced elsewhere (module/system, module/systemmetric, module/ratelimiter — out of slice).

---

## 9. Redis key catalog

| Key / stream | Redis type | Written by | Read by |
|---|---|---|---|
| `task:delayed` | sorted-set | `infra/redis/task_queue.go:69` (`SubmitDelayedTask`); cron reschedule `consumer/task.go:201` | Scheduler `ProcessDelayedTasks`; `Expedite` (`infra/redis/task_queue.go:82`); `CancelTask` (`consumer/task.go:451`) |
| `task:ready` | list | scheduler promote; `SubmitImmediateTask` (`task_queue.go:35`) | `GetTask` BRPOP (`task_queue.go:43`); `CancelTask` |
| `task:dead` | sorted-set | `HandleFailedTask` (`task_queue.go:53`) | `CancelTask` |
| `task:index` | hash | every enqueue / dead-letter / promotion | `CancelTask` via `GetTaskQueue` |
| `task:concurrency_lock` | counter | `AcquireConcurrencyLock` / `ReleaseConcurrencyLock` (`consumer/task.go:43,228`) | same |
| `trace:%s:log` | stream (`consts.StreamTraceLogKey`) | `common/task.go:168` (`EmitTaskScheduled`); `consumer/task.go` publishEvent; `consumer/k8s_handler.go:443,556`; `consumer/monitor.go:110,183`; `consumer/restart_pedestal.go:153,165`; `module/task/service.go:137` (expedite) | `module/trace/handler.go:146` SSE; `trace.Service.ReadTraceStreamMessages` |
| `group:%s:log` | stream (`consts.StreamGroupLogKey`) | `service/consumer/trace.go` when traces go terminal | `module/group/handler.go:110` SSE; `group.Service.ReadGroupStreamMessages` |
| `notifications:global` | stream (`consts.NotificationStreamKey`) | outside slice (module/system, ratelimiter GC, …) | `module/notification/handler.go:60` SSE |
| `monitor:namespaces` | set (`consts.NamespacesKey`) | `consumer/monitor.go` via `namespaceCatalogStore.seed` | `monitor.listNamespaces`, `InitializeNamespaces`, `RefreshNamespaces` |
| `monitor:ns:%s` | hash (`consts.NamespaceKeyPattern`) | `namespaceLockStore.acquire/release/write` + `namespaceStatusStore.set` | `namespaceLockStore.read*`, `namespaceStatusStore.get` |
| `token_bucket:restart_service` / `:build_container` / `:algo_execution` | set (Lua-scripted) | `rate_limiter_store.go:31` SADD | `rate_limiter_store.go:28,48,57` SCARD/SREM |
| `injection:algorithms` | hash (`consts.InjectionAlgorithmsKey`) | `module/injection/service.go:298` (per groupID) | `consumer/redis.go:41` (`loadCachedInjectionAlgorithms`); `module/trace/service.go:68-70` |
| `joblogs:<taskID>` | Pub/Sub channel | `logreceiver/receiver.go` via `publisher.Publish(ctx, channel, …)` (channel prefix `PubSubChannelPrefix` = `joblogs`, `receiver.go:33`) | `module/task/queue_store.go:22` (`SubscribeJobLogs`) — used by WS log stream |
| `config:updates:response` | Pub/Sub channel (`consts.ConfigUpdateResponseChannel`) | `service/common/config_registry.go:84` (`PublishWrapper`) | out of slice (module/system, ratelimiter services subscribe) |
| `last_batch_info` | string (const `consumer/task.go:42`) | declared only; no active write in the slice — looks unused / legacy |

---

## 10. K8s label / annotation schema

Set by **`fault_injection.go:221-228`** on CRDs (merged with `task.GetLabels()` which adds the identifiers below):
- `rcabench_app_id` → `consts.AppID` (`K8sLabelAppID`).
- `batch_id` → `batchID` (`CRDLabelBatchID`).
- `is_hybrid` → strconv bool (`CRDLabelIsHybrid`).
- Identifiers added by `dto.UnifiedTask.GetLabels()`: `task_id`, `task_type`, `trace_id`, `group_id`, `project_id`, `user_id` (consts `JobLabelTaskID/TraceID/GroupID/ProjectID/UserID/TaskType` — same keys reused for CRD and Job).

CRD annotations (`fault_injection.go:208-217`):
- `task_carrier` (`consts.TaskCarrier`) — OTel text-map carrier JSON.
- `trace_carrier` (`consts.TraceCarrier`) — OTel text-map carrier JSON.
- `benchmark` (`consts.CRDAnnotationBenchmark`) — JSON-marshaled `dto.ContainerVersionItem`.

Job labels (`build_datapack.go:79-86`, `algo_execution.go:131-138`):
- `rcabench_app_id`.
- `datapack` → datapack name (`JobLabelDatapack`).
- `dataset_id` → `JobLabelDatasetID` (BuildDatapack only).
- `execution_id` → `JobLabelExecutionID` (RunAlgorithm only).
- Plus the six identifier labels from `task.GetLabels()`.

Job annotations:
- `task_carrier`, `trace_carrier` (via `task.GetAnnotations`).
- `datapack` (`JobAnnotationDatapack`) — JSON `dto.InjectionItem` (`build_datapack.go:77`, `algo_execution.go:129`).
- `algorithm` (`JobAnnotationAlgorithm`) — JSON `dto.ContainerVersionItem` (`algo_execution.go:123`).

Parsed back in `k8s_handler.go` by `parseAnnotations` (`k8s_handler.go:712-765`), `parseTaskIdentifiers` (`k8s_handler.go:767-818`), `parseCRDLabels` (`k8s_handler.go:820-845`), `parseJobLabels` (`k8s_handler.go:847-866`).

---

## 11. Task type inventory

Values from `consts/consts.go:282-291` (`iota`-based):

| Value | Enum | Dispatch case | Executor |
|---|---|---|---|
| 0 | `TaskTypeBuildContainer` | `distribute_tasks.go:35` | `executeBuildContainer` (`build_container.go:43`) |
| 1 | `TaskTypeRestartPedestal` | `distribute_tasks.go:37` | `executeRestartPedestal` (`restart_pedestal.go:33`) |
| 2 | `TaskTypeFaultInjection` | `distribute_tasks.go:39` | `executeFaultInjection` (`fault_injection.go:100`) |
| 3 | `TaskTypeRunAlgorithm` | `distribute_tasks.go:43` | `executeAlgorithm` (`algo_execution.go:61`) |
| 4 | `TaskTypeBuildDatapack` | `distribute_tasks.go:41` | `executeBuildDatapackWithDeps` (`build_datapack.go:54`) |
| 5 | `TaskTypeCollectResult` | `distribute_tasks.go:45` | `executeCollectResult` (`collect_result.go:27`) |
| 6 | `TaskTypeCronJob` | **NO dispatch case** | falls through to `default: unknown task type` (`distribute_tasks.go:47`). Only handled indirectly — `processDelayedTasks` reschedules it via `CronExpr` (`task.go:184-209`), and `common.SubmitTaskWithDB` branches on it when computing `ExecuteTime`. So `TaskTypeCronJob` is still missing in the dispatcher. |

---

## 12. Cross-module + cross-layer imports

Deduped symbols imported from outside the slice:

- `aegis/consts` — pervasive (every file).
- `aegis/dto` — pervasive.
- `aegis/model` — all module repositories + state updaters.
- `aegis/utils` — pervasive helpers (`StringPtr`, `IntPtr`, `ConvertToType`, `MergeSimpleMaps`, `GenerateULID`, `GetCallerInfo`, `GenerateServiceToken`, `MakeSet`, `GetPointerIntFromMap`, etc.).
- `aegis/config` — `GetDetectorName`, `GetAllNamespaces`, `GetChaosSystemConfigManager`, `GetInt`, `GetString`, `SetDetectorName`, `SetViperValue`, `SetChaosConfigDB`.
- `aegis/middleware` — `GetCurrentUserID`, `SpanContextKey` (handlers only).
- `aegis/httpx` — `HandleServiceError`, `ParsePositiveID` (handlers only).
- `aegis/searchx` — `module/injection/repository.go`.
- `aegis/tracing` — `WithSpan`, `SetSpanAttribute` (consumer).
- `aegis/infra/redis` — `Gateway`, queue constants (DelayedQueueKey etc.).
- `aegis/infra/loki` — client + `QueryOpts` (injection, task).
- `aegis/infra/k8s` — `Gateway`, `Controller`, `JobConfig`, `VolumeMountConfig`, `VolumeMountName`.
- `aegis/infra/helm` — `Gateway` (restart_pedestal).
- `aegis/infra/buildkit` — `Gateway` (build_container).
- `aegis/infra/db` — `NewDatabaseConfig("clickhouse")` (build_datapack).
- `aegis/infra/etcd` — `Gateway` (worker Lifecycle, config_listener).
- `aegis/infra/runtime` — `runtimeinfra.Module` (runtime_stack).
- `aegis/infra/chaos` — `chaos.Module` (runtime_stack, producer).
- `aegis/internalclient/orchestratorclient` — `Client`, `Module`, `Enabled()`, `CreateExecution`/`Injection`/etc. (owner_adapter).
- `aegis/module/container` — `NewRepository`, `ResolveContainerVersions`, `ListContainerVersionEnvVars`, `ListHelmConfigValues` (injection.Service, execution.Service, consumer/k8s_handler).
- `aegis/module/label` — `NewRepository`, `CreateOrUpdateLabelsFromItems` (injection, execution).
- `aegis/module/ratelimiter` — `NewService.GC` (initialization/consumer).
- `aegis/module/group` — imported by `service/consumer/trace.go` (group-stream event construction).
- `aegis/module/dataset`, `aegis/module/container` — used by `initialization/producer.go` for seeding.
- `aegis/interface/http`, `aegis/interface/grpc/runtime` — wired via `app/*.go` (referenced as fx modules).
- External: `github.com/OperationsPAI/chaos-experiment/handler` (alias `chaos`), `.../pkg/guidedcli`, `github.com/gin-gonic/gin`, `github.com/gin-contrib/sse`, `github.com/gorilla/websocket`, `github.com/redis/go-redis/v9`, `github.com/sirupsen/logrus`, `github.com/google/uuid`, `github.com/robfig/cron/v3`, `go.uber.org/fx`, `go.opentelemetry.io/otel/*`, `k8s.io/api/*`, `sigs.k8s.io/controller-runtime/pkg/log`, `gorm.io/gorm`, `github.com/prometheus/client_golang`.

---

## 13. Surprises / dead code / partial migration signs

1. `consts.TaskTypeCronJob` (7th task type) has **no dispatch case** in `distribute_tasks.go:34-49` — old bug survives the refactor. `dispatchTask` returns `"unknown task type: 6"` if a cron job actually reaches the worker instead of being rescheduled.
2. `LastBatchInfoKey = "last_batch_info"` constant is declared (`service/consumer/task.go:42`) but never written or read anywhere in the slice — dead constant.
3. Two parallel routes for runtime writes: local `executionOwnerAdapter`/`injectionOwnerAdapter` fall back to in-process service calls, whereas `RemoteOwnerOptions()` (`owner_adapter.go:166-171`) decorates to require orchestrator RPC. This coexistence (`requireRemote` bool) is the DI seam between the monolith and the split-out runtime-worker-service — not fully finished (both paths still shipped).
4. `GET /api/v2/injections/metadata` (`handler.go:286`) and `POST /api/v2/injections/translate` (`handler.go:335`) return `http.StatusGone` with body `endpoint removed; migrate to /inject with GuidedConfig`. Swagger annotations still describe them as live; need pruning.
5. `Service.GetMetadata` in injection (`service.go:477-479`) returns `(nil, nil)` — stub left after the `/metadata` endpoint was retired.
6. `consumerDetachedContext()` returns `context.TODO()` (`consumer/redis.go:12-14`) — a TODO comment is embedded by way of the function's contract; no propagation from worker ctx into detached CRD/Job callbacks. Suggests the OTel carriers on annotations are load-bearing because the Go context is intentionally lost.
7. `service/consumer/jvm_runtime_mutator.go` (102 lines) is not referenced by the dispatch chain read in this slice — likely used only from `chaos-experiment` plumbing inside `fault_injection.go`'s InjectionConf; confirm whether this is active or stale.
8. `module/trace/service.go:80-83` silently filters out the detector algorithm from the trace's "algorithms to wait for" set — coupling `module/trace` to `config.GetDetectorName()`. Could live in consumer instead.
9. `k8s_handler.go:822-829`: `batchID` absence check is `if !ok && batchID == ""` (should be `||`) — both branches behave the same because `ok=false` implies `batchID==""`, but the condition as written is tautological and easy to misread.
10. `service/consumer/task.go:343` in `executeTaskWithRetry` immediately discards the inner `ctxWithCancel.cancel`: `_ = cancel` — this is a context leak per retry attempt. Silent bug, not a refactor-related regression but worth flagging.
11. `consts.JobLabelTaskID = "task_id"` (no prefix) collides with generic K8s label hygiene; in combination with `K8sLabelAppID = "rcabench_app_id"` it suggests labels were renamed partway through — mix of prefixed and unprefixed keys.
12. `interface/worker/module.go:58-68` builds `commonservice.NewConfigUpdateListener(ctx, params.DB, params.Etcd)` inline inside the lifecycle start rather than injecting it via fx; `app/producer.go:53` does the same trick. Two call sites => small duplication, probably an intentional simplification because the listener's ctx must match the lifecycle ctx.
