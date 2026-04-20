# AegisLab Critical Call Paths (v2)

> Archival note: this file was not fully revalidated after the phase-2 gRPC collapse and phase-6 module-wiring cleanup. Treat `docs/code-topology/README.md`, `docs/code-topology/slices/01-app-wiring.md`, and `docs/code-topology/slices/06-grpc-interfaces.md` as the current topology source of truth.

All citations relative to `/home/ddq/AoyangSpace/aegis/AegisLab/src/`. Cross-reference the
7 raw agent reports under `slices/` for exhaustive edges behind each flow.

---

## 1. End-to-end fault-injection pipeline

The main workflow: one `POST /api/v2/projects/:pid/injections/inject` eventually produces a
fully collected algorithm result. It still spans `module/` (producer-side), `service/common`
(queue), `service/consumer` (async workers + K8s callbacks), and `interface/worker+controller`
(lifecycle).

```
[HTTP]  POST /api/v2/projects/:pid/injections/inject
       router (portal.go / sdk.go) ─▶ module/injection/handler.SubmitProjectFaultInjection (handler.go:202)
           │ middleware: JWTAuth + RequireProjectInjectionExecute | RequireAPIKeyScopesAny
           ▼
       injection.Service.SubmitFaultInjection (service.go:152-359)
           │ legacy path: req.ResolveSpecs(FriendlySpecToNode)  OR
           │ guided path: parseBatchGuidedSpecs → guidedcli.BuildInjection(ctx, cfg)
           │ resolves pedestal+benchmark via container.NewRepository.ResolveContainerVersions
           │ caches algos at redis:  SetHashField("injection:algorithms", groupID, items)
           │ dedups via repository.ListExistingEngineConfigs
           │ builds dto.UnifiedTask{Type: TaskTypeRestartPedestal, Immediate: false, …}
           ▼
       common.SubmitTaskWithDB (service/common/task.go:56-151)
           │ UpsertTrace + UpsertTask in one GORM txn
           │ Redis: redisGateway.SubmitDelayedTask → ZADD task:delayed + HSET task:index
           │ Emits EventTaskScheduled to trace:<traceID>:log

[Scheduler]  consumer.StartScheduler (task.go:154) [1s ticker]
           ▼  redisGateway.ProcessDelayedTasks (Lua: ZRANGEBYSCORE + LPUSH task:ready + HSET)

[Worker]  consumer.ConsumeTasks (task.go:219-246)  ← launched by interface/worker/module.go:76
           │ AcquireConcurrencyLock (global cap 20)
           │ BRPOP task:ready 30s
           ▼  go processTask → executeTaskWithRetry → dispatchTask (distribute_tasks.go:15-56)
                                     │
                                     ▼  TaskTypeRestartPedestal
                  executeRestartPedestal (restart_pedestal.go:33-199)
                      │ acquire token from token_bucket:restart_service (Lua)
                      │ monitor.GetNamespaceToRestart → atomic lock at monitor:ns:<ns>
                      │ helm.Gateway.Install (pedestal chart)
                      │ common.ProduceFaultInjectionTasksWithDB  (re-submits TaskTypeFaultInjection
                      │                                           delayed until injectTime)
                                     ▼
                     executeFaultInjection (fault_injection.go:100-282)
                      │ monitor.CheckNamespaceToInject (re-validate lock)
                      │ decode payload.nodes (legacy) OR payload.guidedConfigs (new)
                      │ set CRD annotations: task_carrier, trace_carrier, benchmark (JSON ContainerVersionItem)
                      │ set CRD labels: rcabench_app_id, batch_id, is_hybrid + task.GetLabels (6 ids)
                      │ chaos.BatchCreate(ctx, injectionConfs, system, namespace, annotations, labels)
                      │ deps.InjectionOwner.CreateInjection (local service OR orchestrator RPC)

[K8s CRD informer (interface/controller/module.go:52)]
           ▼  controller detects Run→Stop + AllInjected → enqueues CheckRecovery (AddAfter)
           ▼  workqueue → CheckRecovery succeeds → Callback.HandleCRDSucceeded
                  (service/consumer/k8s_handler.go:256-347)
                      │ parseAnnotations/parseTaskIdentifiers rebuild OTel spans from carriers
                      │ batchManager.incrementBatchCount (hybrid-batch path)
                      │ store.updateInjectionState(DatapackInjectSuccess)
                      │ store.updateInjectionTimestamp(startTime, endTime)
                      │ updateTaskState → EventFaultInjectionCompleted
                      │ common.SubmitTaskWithDB (TaskTypeBuildDatapack, ParentTaskID=taskID)
                                     ▼
                  executeBuildDatapackWithDeps (build_datapack.go:54-98)
                      │ k8sGateway.CreateJob with JobLabelDatapack/DatasetID + task labels
                      │ JobAnnotationDatapack = JSON InjectionItem
                                     ▼
           ▼  Job succeeds → Callback.HandleJobSucceeded (k8s_handler.go:525-710)
                      │ branch TaskTypeBuildDatapack (line 529-594):
                      │   updateInjectionState(DatapackBuildSuccess)
                      │   common.SubmitTaskWithDB(TaskTypeRunAlgorithm)   // detector algorithm
                                     ▼
                  executeAlgorithm (algo_execution.go:61-155)
                      │ acquire token from token_bucket:algo_execution
                      │ deps.ExecutionOwner.CreateExecution (local OR orchestrator RPC)
                      │ k8sGateway.CreateJob (JobAnnotationAlgorithm JSON ContainerVersionItem)
                                     ▼
           ▼  Job succeeds → branch TaskTypeRunAlgorithm (k8s_handler.go:596-667)
                      │ rateLimiter.ReleaseToken
                      │ if container == detector: updateInjectionState(DatapackDetectorSuccess)
                      │ else:                    updateExecutionState(ExecutionSuccess)
                      │ common.SubmitTaskWithDB(TaskTypeCollectResult)
                                     ▼
                  executeCollectResult (collect_result.go:27-…)
                      │ if detector:
                      │   repository.ListDetectorResultsByExecutionID
                      │   if hasIssues: loadCachedInjectionAlgorithms → fan out
                      │                 common.SubmitTaskWithDB(TaskTypeRunAlgorithm) per algo
                      │   else: EventDatapackNoAnomaly / EventDatapackNoDetectorData
                      │ else (RCA algo):
                      │   repository.ListGranularityResultsByExecutionID
                      │   EventAlgoResultCollection / EventAlgoNoResultData

[Trace finalization]  service/consumer/trace.go
           │ tryUpdateTraceStateCore re-infers Trace.State from Task subtree
           │ publishGroupStreamEvent XADD group:<groupID>:log when terminal
```

**Takeaways**:

- **No workflow engine**: the chain is *entirely* K8s CRD/Job lifecycle callbacks +
  `common.SubmitTaskWithDB`. `k8s_handler.go` is the orchestration engine.
- **Runtime-split seam**: every state write (`CreateInjection`, `UpdateInjectionState`,
  `CreateExecution`, `UpdateExecutionState`, `UpdateInjectionTimestamps`) goes through
  `ExecutionOwner` / `InjectionOwner` adapters (`service/consumer/owner_adapter.go:29-79`).
  In the monolith or `both` mode they call the local `injection.Service` / `execution.Service`;
  in the dedicated `runtime-worker-service` the `RemoteOwnerOptions()` fx.Decorate forces
  them to call `orchestratorclient.Client` instead. **The split is half-done — both paths
  are still shipped.**
- **Hybrid batches**: `batchManager` (in-memory only, `service/consumer/fault_injection.go:42`)
  gates the BuildDatapack submission until all CRDs in the batch reach terminal state. **State
  lost on worker restart.**
- Every state transition emits a `consts.EventType` to `trace:<traceID>:log` for SSE replay
  (`consumer/task.go:481` publisher + ~30 other call sites).

## 2. HTTP request lifecycle (producer / api-gateway mode)

```
interface/http/server.go  ← gin.Default() + router.New(handlers, middlewareService)

router.New global chain (router.go:25-32):
    InjectService(middlewareService)   — stashes dbBackedMiddlewareService in gin.Context
    RequestID()                        — X-Request-Id propagation
    GroupID()                          — uuid for POSTs, X-Group-ID header
    SSEPath()                          — sets SSE headers on ^/stream(/.*)?$  (SEE orphans §E5)
    cors.New(AllowAllOrigins+Credentials)  — permissive; see orphans §E4
    TracerMiddleware()                 — OTel span "rcabench/group" per request

Then: v2 := router.Group("/api/v2") → split into 4 audience files (URL-transparent):
    public.go   /auth/login, /auth/register, /auth/refresh, /auth/logout (JWT), /auth/profile
    sdk.go      /auth/api-key/token, /sdk/*, project-scoped executions/injections
                - middleware: JWTAuth + RequireAPIKeyScopesAny("sdk:*" | "sdk:evaluations:*" …)
                - result-upload endpoints use RequireServiceTokenAuth (K8s job → API)
    portal.go   /containers/*, /datasets/*, /projects/*, /teams/*, /labels/*, /tasks/*,
                /pedestal/*, /rate-limiters/*, /traces/*, /groups/*, /notifications/stream,
                /evaluations/:id DELETE, /api-keys/* (with RequireHumanUserAuth)
    admin.go    /users/*, /roles/*, /permissions/*, /resources/*, /systems/*,
                /system/{metrics, audit, configs, health, monitor}

JWT flow (middleware/auth.go:18-66):
    Bearer token → utils.ValidateToken (user) OR utils.ValidateServiceToken (K8s job)
    Sets ctx keys: user_id, username, email, is_active, is_admin, user_roles,
                   auth_type, api_key_id, api_key_scopes, token_type, is_service_token

Permission flow (middleware/permission.go):
    RequireX pre-built vars → dbBackedMiddlewareService.CheckUserPermission
    Scope gating for API keys: scopeAllowsPermission + apiKeyScopeMatchesPermission
    Team/Project access: RequireTeamAccess, RequireProjectAccess → project/team service helpers

API-key scope (middleware/api_key_scope.go, NEW):
    dot-colon glob: "*" matches all; "sdk:*" matches "sdk:evaluations:read"
    RequireAPIKeyScopesAny(targets...) — applies ONLY when auth_type == "api_key"
    RequireHumanUserAuth — rejects api_key + service tokens (used on /api-keys, /auth/*)
```

**Note**: `middleware.AuditMiddleware()` is defined (`middleware/audit.go:22`) but **not wired into
`router.New`** — audit logging is silently disabled. `middleware/ratelimit.go` buckets are declared
but mounted nowhere. See `orphans.md §D1, §D2`.

## 3. Task queue internals

```
enqueue:
    common.SubmitTaskWithDB
        → MySQL: UpsertTrace + UpsertTask (single GORM txn)
        → Redis: Immediate? SubmitImmediateTask (LPUSH task:ready + HSET task:index)
                 else:       SubmitDelayedTask  (ZADD task:delayed + HSET task:index)
                              + EmitTaskScheduled (XADD trace:<traceID>:log EventTaskScheduled)

scheduler (consumer mode + both + runtime-worker):
    consumer.StartScheduler (task.go:154)  — 1 s ticker
        redisGateway.ProcessDelayedTasks (Lua atomic: ZRANGEBYSCORE → LPUSH task:ready → HSET task:index)
        cron branch: common.CronNextTime → SubmitDelayedTask (re-schedule)

worker loop:
    consumer.ConsumeTasks
        INCR task:concurrency_lock  (cap 20, AcquireConcurrencyLock)
        BRPOP task:ready 30s        (GetTask)
        go processTask:
            extractContext (OTel trace + task spans from TaskCarrier/TraceCarrier/GroupCarrier)
            executeTaskWithRetry (RetryPolicy MaxAttempts, exp backoff)
                registerCancel in taskCancelFuncs  — CancelTask removes from queues + ctx.cancel
                ├─ TaskTypeBuildContainer   → executeBuildContainer (build_container.go)
                ├─ TaskTypeRestartPedestal  → executeRestartPedestal
                ├─ TaskTypeFaultInjection   → executeFaultInjection
                ├─ TaskTypeBuildDatapack    → executeBuildDatapackWithDeps
                ├─ TaskTypeRunAlgorithm     → executeAlgorithm
                ├─ TaskTypeCollectResult    → executeCollectResult
                └─ TaskTypeCronJob          → ❌ NO dispatch case (orphans.md §B1)
            handleFinalFailure → ZADD task:dead + updateTaskState(TaskError) + EventJobFailed
        DECR task:concurrency_lock
```

## 4. Rate-limiter lifecycle (Redis SET semaphores)

Three buckets, all Lua-scripted in `service/consumer/rate_limiter_store.go:20-46`:

| Bucket Redis key | Default cap | Used by |
|---|---|---|
| `token_bucket:restart_service` | 2 | `executeRestartPedestal` |
| `token_bucket:build_container` | 3 | `executeBuildContainer` |
| `token_bucket:algo_execution` | 5 | `executeAlgorithm` |

Caps are dynamic-config overridable via `rate_limiting.max_concurrent_*` keys (`NewRestartPedestalRateLimiter`, `rate_limiter.go:179-207`).

**Acquire** (Lua): `if SCARD < cap then SADD taskID; return 1 else return 0`.
If 0, the executor calls `rescheduleXXXTask` which computes a backoff and re-submits delayed.

**Release** on success: `SREM taskID` — from `HandleJobSucceeded`/`HandleJobFailed` or inline after the executor returns.

**Leak detection** (`module/ratelimiter/service.go.GC`, invoked from `initialization.InitializeConsumer`):
iterates `SMEMBERS` for each known bucket, reads `model.Task.state`. If task missing OR state ∈ {Completed, Error, Cancelled} → `SREM`. Exposed HTTP surface:

- `GET  /api/v2/rate-limiters` — list holders with `LEAKED` flag
- `DELETE /api/v2/rate-limiters/:bucket` — force reset (admin only)
- `POST /api/v2/rate-limiters/gc` — manual GC (admin only)

Auto-GC now runs at consumer startup inside `interface/worker/module.go` via `initialization.InitializeConsumer`.

## 5. OTLP log pipeline

```
[K8s Job]  algorithm container emits OTLP logs (protobuf or JSON, optional gzip, body ≤ 5 MB)
              │
              ▼  HTTP POST /v1/logs  (port = dynamic config "otlp_receiver.port", default 4319)
[Receiver] service/logreceiver/receiver.go
              (launched by interface/receiver/module.go:39 fx OnStart)
              │
              ├── parseOTLPLogs → dto.LogEntry{Timestamp, Line, TaskID, TraceID, JobID, Level}
              │   entries with empty task_id are dropped (parser.go:28)
              │
              ▼  publishLogEntry
           redis.Gateway.Publish("joblogs:<TaskID>", logLine)
              │     ← PUB/SUB channel, NOT a stream. Lost if no subscriber.
              ▼
[Subscriber] module/task/TaskLogService.StreamLogs (log_service.go:27)
              │   Loki.QueryJobLogs backfill ({app="rcabench"} | task_id=…)
              │   then Subscribe joblogs:<taskID> for realtime
              │   plus polling DB for terminal state
              ▼
[Handler]  module/task/handler.GetTaskLogsWS  (GET /api/v2/tasks/:id/logs/ws)
              │   WebSocket upgrade; auth via ?token= query param
              │   emits dto.WSLogMessage frames (history | realtime | end | error)
              ▼
[Client]  aegisctl task logs / frontend tasks detail page
```

## 6. Config hot-reload (etcd + `dynamic_configs` table)

```
[startup]
    service/initialization/{producer,consumer}.go invoke common.RegisterGlobalHandlers +
      common.RegisterConsumerHandlers (consumer-side) + activateConfigScope(scope, listener)
    common.NewConfigUpdateListener creates a listener on etcd prefixes:
      ConfigEtcdProducerPrefix | ConsumerPrefix | GlobalPrefix

    Global handlers registered:
      algoConfigHandler  — on key "algo.detector" → config.SetDetectorName
      (consumer) chaosSystemCountHandler  — on "injection.system.count"
      (consumer) rateLimitingConfigHandler — on "rate_limiting.max_concurrent_*"

[runtime update: HTTP]
    PATCH /system/configs/:id  (handlers via system module routes)
      → dynamicConfigService.UpdateValue (producer side)
      → repository.UpdateConfig  (adds ConfigHistory row)
      → etcd.Gateway.Put(key, newValue)

[watcher fires in each subscriber process]
    common/config_listener.go.watchPrefix
      → handleConfigChange matches (scope, category) to registered ConfigHandler
      → handler runs (e.g. SetDetectorName)
      → PublishWrapper pubs dto.ConfigUpdateResponse on
        Redis pub/sub "config:updates:response" so callers can wait for ACK
```

The detector name lives in `src/config/config.go:17` as an `atomic.Value` — read from hot paths
(task dispatch, k8s_handler, trace stream filter, algo_execution) and written by the etcd handler.

## 7. Streaming endpoints (3 SSE + 1 WS)

| Endpoint | Channel type | Backing store | Event semantics |
|---|---|---|---|
| `GET /api/v2/traces/:id/stream` | SSE (gin-contrib/sse) | Redis XStream `trace:<id>:log` | replay from start + XREAD block; terminates on `consts.EventEnd` when Trace.state terminal |
| `GET /api/v2/groups/:id/stream` | SSE | Redis XStream `group:<id>:log` | replay + tail; `group.Service.NewGroupStreamProcessor` emits group-level terminal events only |
| `GET /api/v2/notifications/stream` | SSE | Redis XStream `notifications:global` | tail-only (**no frontend caller — see orphans.md**) |
| `GET /api/v2/tasks/:id/logs/ws` | WebSocket (gorilla/websocket) | Pub/Sub `joblogs:<id>` + Loki historical | auth via `?token=` query; dto.WSLogMessage frames |

**Polling-RPC twist**: the gRPC counterparts on `orchestrator-service` (`ReadTraceStreamMessages`,
`ReadGroupStreamMessages`, `ReadNotificationStreamMessages`) are **not** streaming RPCs; they are
unary calls with a `block_millis` parameter that inner-BLOCK via `XREAD`. Clients must poll. See
`slices/06-grpc-interfaces.md §1`.

## 8. Entry-point cheat-sheet (where to start reading)

| Start from | To understand |
|---|---|
| `main.go` + `cmd/<svc>/main.go` | the 9 modes and how they map to fx options |
| `app/app.go` | the 6 `*Options()` building blocks |
| `app/gateway/options.go` | the 20 `fx.Decorate`s that implement edge-routing |
| `app/runtime_stack.go` | the consumer runtime worker composition |
| `router/{public,sdk,portal,admin}.go` | the full `/api/v2/*` route table, audience-split |
| `service/common/task.go:56` | the single `SubmitTaskWithDB` function |
| `service/consumer/distribute_tasks.go` | the task-type switch (6 types handled, 1 missing) |
| `service/consumer/k8s_handler.go` | workflow chain implementation (CRD+Job → next task) |
| `service/consumer/owner_adapter.go` | local/remote seam for runtime-worker mode |
| `module/injection/submit.go` + `service.go` | inject submission and dedup logic |
| `model/entity.go` + `view.go` | every MySQL table + views |
| `consts/consts.go` | every enum, Redis key pattern, label, EventType |
| `consts/system.go` | RBAC matrix (roles, actions, scopes, PermXxx rules, SystemRolePermissions) |
| `middleware/deps.go` | `dbBackedMiddlewareService` — where permission checks actually run |
| `middleware/api_key_scope.go` | new API-key scope glob matcher |
