# AegisLab Critical Call Paths

All citations relative to `/home/ddq/AoyangSpace/aegis/AegisLab/src/`.
Cross-reference `slices/*.md` for the exhaustive edge lists behind each flow.

## 1. End-to-end fault-injection pipeline (the core workflow)

This is the chain that turns a single `POST /api/v2/projects/:pid/injections/inject` into a fully-collected algorithm result. It spans producer, consumer, K8s controller, and rate-limiter.

```
[HTTP]  POST /api/v2/projects/:pid/injections/inject
        handlers/v2/injections.go → producer.FriendlySpecToNode (legacy) | guidedcli.BuildInjection (new)
                                  → producer.ProduceRestartPedestalTasks  (injection.go:806)
                                        │ dedup via repository.ListExistingEngineConfigs
                                        │ store group→algorithms in Redis: client.SetHashField(InjectionAlgorithmsKey, groupID, items)
                                        ▼
                                   common.SubmitTask( TaskTypeRestartPedestal )
                                        │ repository.UpsertTrace + UpsertTask (MySQL)
                                        │ LPUSH task:ready (immediate=true) OR ZADD task:delayed
                                        ▼
[Consumer] consumer.ConsumeTasks    task.go:207  ← BRPOP task:ready
                                        │ AcquireConcurrencyLock (global cap = 20)
                                        │ executeTaskWithRetry (retries per RetryPolicy)
                                        ▼
               dispatchTask switch (distribute_tasks.go:15-56)
                                        │
                                        ▼  TaskTypeRestartPedestal
               executeRestartPedestal  restart_pedestal.go:31
                                        │ monitor.GetNamespaceToRestart (Redis HSet monitor:ns:<ns>)
                                        │ GetRestartPedestalRateLimiter.AcquireToken (Redis SADD token_bucket:restart_service, cap=2)
                                        │ client/helm.go → helm install pedestal chart
                                        │ common.ProduceFaultInjectionTasks (restart_pedestal.go:179)
                                        ▼
                                   common.SubmitTask( TaskTypeFaultInjection,  delayed until injectTime )
                                        ▼
               executeFaultInjection   fault_injection.go:111
                                        │ monitor.CheckNamespaceToInject
                                        │ chaos.BatchCreate → creates one or more Chaos Mesh CRDs (DNSChaos/NetworkChaos/…)
                                        │   CRD annotations: task_carrier, trace_carrier, benchmark (JSON)
                                        │   CRD labels: rcabench_app_id, task_id, trace_id, group_id, project_id,
                                        │               user_id, task_type, batch_id, is_hybrid
                                        │ repository.CreateInjection (FaultInjection row, state=InjectSuccess)
                                        ▼
               (CRD runs in Chaos Mesh)
                                        ▼
[k8s ctlr]  client/k8s/controller.go   CRD informer UpdateFunc detects Run→Stop
                                        │ controller.go:389 → enqueue CheckRecovery with AddAfter(duration)
                                        │ processQueueItem  (controller.go:441-485)  — once AllRecovered
                                        ▼
               Callback.HandleCRDSucceeded (service/consumer/k8s_handler.go:230)
                                        │ parse labels+annotations to recover task context
                                        │ batchManager.incrementBatchCount (for hybrid multi-CRD batches)
                                        │ if NOT hybrid OR batch complete:
                                        │   updateInjectionState → DatapackInjectSuccess
                                        │   common.SubmitTask( TaskTypeBuildDatapack )  (line 288-302)
                                        ▼
               executeBuildDatapack   build_datapack.go:55
                                        │ k8s.CreateJob with benchmark container, datapack name in labels/annotations
                                        ▼
               (Job runs, writes parquet files into jfs.dataset_path/<datapack.Name>/)
                                        ▼
               Callback.HandleJobSucceeded (k8s_handler.go:487, BuildDatapack branch 529-594)
                                        │ updateInjectionState → DatapackBuildSuccess
                                        │ map detector container ref → ContainerVersion
                                        │ common.SubmitTask( TaskTypeRunAlgorithm )  with detector=config.GetDetectorName()
                                        ▼
               executeAlgorithm       algo_execution.go:62
                                        │ GetAlgoExecutionRateLimiter.AcquireToken (Redis SADD token_bucket:algo_execution, cap=5)
                                        │ createExecution row (DB)
                                        │ k8s.CreateJob with algorithm container + init-container mkdir OUTPUT_PATH
                                        ▼
               Callback.HandleJobSucceeded (RunAlgorithm branch 596-667)
                                        │ rateLimiter.ReleaseToken (SREM)
                                        │ if algorithm == detector: updateInjectionState → DatapackDetectorSuccess
                                        │ updateExecutionState → ExecutionSuccess
                                        │ common.SubmitTask( TaskTypeCollectResult )
                                        ▼
               executeCollectResult   collect_result.go:27
                                        │ detector branch:
                                        │   repository.ListDetectorResultsByExecutionID
                                        │   if hasIssues AND client.CheckCachedField(InjectionAlgorithmsKey, groupID):
                                        │     for each algo in HGETALL injection:algorithms[groupID]:
                                        │       produceAlgorithmExeuctionTask
                                        │       → common.SubmitTask( TaskTypeRunAlgorithm )  (fan-out)
                                        │   else: EventDatapackNoAnomaly / EventDatapackNoDetectorData
                                        │ algorithm branch:
                                        │   repository.ListGranularityResultsByExecutionID
                                        │   emit EventAlgoResultCollection or EventAlgoNoResultData
                                        ▼
               updateTaskState → updateTraceState (trace.go:84)
                                        │ tryUpdateTraceStateCore re-infers Trace.State from Task tree
                                        │ publishGroupStreamEvent XADD group:<groupID>:log  (only when terminal)
```

**Key takeaways**:

- There is no "Workflow" entity. The chain is **CRD+Job callbacks → `common.SubmitTask`** all the way down. `k8s_handler.go` is the orchestration engine.
- Every transition emits an `EventType` onto `trace:<traceID>:log` for SSE replay (`consts/consts.go EventType` + publishers at `consumer/task.go:481` and ~30 other call sites).
- Hybrid batches (multiple CRDs submitted together): `batchManager` (in-memory, `fault_injection.go:42`) gates the BuildDatapack submission until every CRD in the batch reports. **State lost on consumer restart** — see `orphans.md`.

## 2. HTTP request lifecycle (producer mode)

```
gin.Default() ──► cors.New(allow-all)         (router.go:24-27)
            ──► middleware.GroupID            (POSTs get X-Group-ID uuid)
            ──► middleware.SSEPath            (sets SSE headers on /stream(/.*)? paths)
            ──► middleware.TracerMiddleware   (OTel span "rcabench/group")
            │
            ├── /system/*        (router/system.go)
            │      ├── JWTAuth + RequireX     (all except /system/health)
            │      └── handlers/system/*
            │
            ├── /api/v2/*        (router/v2.go, group at v2.go:115)
            │      ├── StartCleanupRoutine() runs ONCE at router build (v2.go:113)
            │      ├── /auth/{login,register,refresh}          → public
            │      ├── /auth/{logout,change-password,profile}  → JWTAuth
            │      ├── /projects/*, /containers/*, …           → JWTAuth + RequireX + (Team|Project)Access for scoped routes
            │      ├── /injections (global)                    → JWTAuth + RequireSystemAdmin on list/search
            │      └── handlers/v2/*
            │
            └── /docs/*any       (swagger)
```

**JWT flow** (`middleware/auth.go:14`): extracts `Authorization: Bearer <token>`, tries `utils.ValidateToken` (user token) then `utils.ValidateServiceToken` (K8s job-to-API). Sets `user_id`, `username`, `email`, `is_active`, `is_admin`, `user_roles`, `token_expires_at`, `token_type` (`user` or `service`), `is_service_token`.

**Permission flow** (`middleware/permission.go`): ~80 pre-built `RequireX` vars all bottom out in `producer.CheckUserPermission(userID, PermissionRule)`. `RequireProjectAccess` / `RequireTeamAccess` additionally call `producer.Is{User|Team}{In|Admin}` and `Is{Team|Project}Public` to support public-resource read paths.

**Audit flow** (`middleware/audit.go:23`): post-flight. Skips GETs and a hard-coded allow-list. `bodyLogWriter` captures the response body. On `status ≥ 400` → `producer.LogFailedAction`; else `producer.LogUserAction`. One stale path string is documented in `orphans.md`.

## 3. Task queue internals

See `slices/04-consumer.md §1` for the full key catalog. Summary:

```
producer-side enqueue
    common.SubmitTask  → MySQL UpsertTrace + UpsertTask
                        → if Immediate: repository.SubmitImmediateTask LPUSH task:ready
                          else:           repository.SubmitDelayedTask  ZADD  task:delayed + EmitTaskScheduled

scheduler loop (consumer mode)
    consumer.StartScheduler  task.go:142   1s ticker
        repository.ProcessDelayedTasks  (Lua: ZRANGEBYSCORE + LPUSH + HSET task:index atomically)
        cron branch: common.CronNextTime → SubmitDelayedTask (reschedule)

worker loop
    consumer.ConsumeTasks   task.go:207
        INCR task:concurrency_lock      (cap 20, AcquireConcurrencyLock)
        BRPOP task:ready 30s            (repository.GetTask)
        go processTask:
            extractContext (OTel trace + task spans from TaskCarrier/TraceCarrier/GroupCarrier)
            executeTaskWithRetry
              ├─ register cancel in taskCancelFuncs (CancelTask removes from queues + aborts context)
              ├─ loop MaxAttempts × dispatchTask
              │     ├─ TaskTypeBuildContainer  → executeBuildContainer (BuildKit → push)
              │     ├─ TaskTypeRestartPedestal → executeRestartPedestal
              │     ├─ TaskTypeFaultInjection  → executeFaultInjection
              │     ├─ TaskTypeBuildDatapack   → executeBuildDatapack  (k8s.CreateJob)
              │     ├─ TaskTypeRunAlgorithm    → executeAlgorithm      (k8s.CreateJob)
              │     ├─ TaskTypeCollectResult   → executeCollectResult  (DB + fan-out)
              │     └─ TaskTypeCronJob         → NOT IN SWITCH (bug/gap — handled only by scheduler reschedule path)
              └─ handleFinalFailure → ZADD task:dead + updateTaskState(TaskError)
            DECR task:concurrency_lock
```

Every state change emits to `trace:<traceID>:log` stream so SSE subscribers see progress in real time.

## 4. Rate-limiter lifecycle (Redis SET semaphores)

**Buckets** (declared in `consts/consts.go`, checked in `rate_limiter.go`):

| Bucket Redis key | Cap | Used by |
|---|---|---|
| `token_bucket:restart_service` | 2 | `executeRestartPedestal` |
| `token_bucket:build_container` | 3 | `executeBuildContainer` |
| `token_bucket:algo_execution` | 5 | `executeAlgorithm` |

**Acquire** (`service/consumer/rate_limiter.go:78-99`): atomic Lua SCRIPT — if `SCARD < cap`: `SADD taskID`. Otherwise returns `false` and the caller calls `rescheduleXXXTask` which computes a backoff and re-submits the task delayed.

**Release** on success (`rate_limiter.go:120-127`): `SREM taskID`. Called from `HandleJobSucceeded` / `HandleJobFailed` / inline in executors.

**Leak detection** (`service/producer/rate_limiter.go:106`): `GCRateLimiters` iterates `SMEMBERS` for each known bucket, looks up `tasks.state` in MySQL. If task row missing OR state ∈ {Completed, Error, Cancelled} → `SREM`. Exposed via:
- HTTP: `POST /api/v2/rate-limiters/gc` (system-admin only)
- Startup hook: `main.go:213 runRateLimiterStartupGC` in consumer/both modes only

**Inspection**: `GET /api/v2/rate-limiters` → `ListRateLimiters` enumerates buckets via `SCAN token_bucket:*`, then per-member resolves `database.Task.state` so the UI can flag `LEAKED` holders.

## 5. OTLP log pipeline (real-time job log tail)

```
[K8s Job]  algorithm container emits OTLP logs
              │
              ▼  HTTP POST /v1/logs (content-type: application/x-protobuf or json; gzip OK)
[Receiver] service/logreceiver/receiver.go  (port = config.otlp_receiver.port or 4319 default)
              │
              ├─ parseOTLPLogs (parser.go)  → dto.LogEntry{Timestamp, Line, TaskID, TraceID, JobID, Level}
              │     drops entries with empty task_id
              │
              ▼  publishLogEntry (receiver.go:246)
           client.RedisPublish("joblogs:<TaskID>", logLine)
              │   (pub/sub, NOT a stream — if no subscriber: log lost)
              ▼
[Subscriber] service/producer/task.go:236  TaskLogStreamer.StreamLogs
              │   also backfills history from Loki via client.NewLokiClient.QueryJobLogs
              │   (LogQL: {app="rcabench"} | task_id=<taskID>)
              ▼
[Handler]  handlers/v2/tasks.go:188  GetTaskLogsWS
              │   websocket upgrade; auth via ?token= query param (WS can't carry headers)
              │   writes dto.WSLogMessage frames: history | realtime | end | error
              ▼
[Client]   aegisctl task logs / frontend tasks detail page
```

## 6. Config hot-reload (etcd + dynamic_configs table)

```
[startup]
  service/initialization/{producer,consumer}.go
    common.registerHandlers(ctx, scope, …Specific)
       ├─ common.RegisterGlobalHandlers  (config_registry.go:52)
       │    registers algoConfigHandler → on key "algo.detector" → config.SetDetectorName
       ├─ (consumer-only) consumer.RegisterConsumerHandlers  (config_handlers.go:18)
       │    registers chaosSystemCountHandler, rateLimitingConfigHandler
       └─ common.GetConfigUpdateListener().EnsureScope(scope)
            ├─ loadScopeFromEtcd (seeds etcd from MySQL defaults for that scope)
            └─ goroutine: watchPrefix → handleConfigChange

[runtime update: HTTP]
  PATCH /system/configs/:id          handlers/system/configs.go
    → producer.UpdateConfigValue
    → repository.UpdateConfig  (MySQL; adds ConfigHistory row)
    → client.EtcdPut(key, newValue)          ← triggers all listeners

[watcher fires]
  common/config_listener.go watchPrefix
    → handleConfigChange picks handler by (scope, category) from configRegistry
    → handler runs (e.g. algoConfigHandler calls config.SetDetectorName)
    → PublishWrapper emits dto.ConfigUpdateResponse on Redis pub/sub
         channel `config:updates:response`   (consts.ConfigUpdateResponseChannel)
    → producer can acknowledge / reflect update in API responses
```

The detector name is `atomic.Value` in `config/config.go:17`, read from hot paths: dto injection/evaluation/execution, producer trace/execution, consumer collect_result/k8s_handler/algo_execution, initialization/common, common/config_registry.

## 7. Streaming endpoints (3 SSE + 1 WS)

All four replay historical Redis data then live-tail the corresponding channel.

| Endpoint | Backing store | Replay query | Live channel | Event shape |
|---|---|---|---|---|
| `GET /api/v2/groups/{id}/stream` | Redis XStream `group:<id>:log` | XRANGE from 0 | XREAD block | `producer.NewGroupStreamProcessor` emits SSE events for group-level terminal transitions |
| `GET /api/v2/traces/{id}/stream` | Redis XStream `trace:<id>:log` | XRANGE | XREAD | `producer.NewStreamProcessor` + `ProcessMessageForSSE`; events per `consts.EventType` |
| `GET /api/v2/notifications/stream` | Redis XStream `notifications:global` | XRANGE | XREAD | `producer.ReadNotificationStreamMessages` |
| `GET /api/v2/tasks/{id}/logs/ws` | Redis Pub/Sub `joblogs:<taskID>` + Loki | Loki `QueryJobLogs` historical | SUBSCRIBE | `dto.WSLogMessage`; auth via `?token=` |

All three SSE endpoints send `consts.EventEnd` as the terminator when the trace/group reaches a terminal state (inferred by `trace.go:inferTraceState`).

## 8. Entry points cheat-sheet

| Start from | To understand |
|---|---|
| `main.go` | mode dispatch, startup order |
| `router/v2.go` | full route table (~140 routes) |
| `service/common/task.go:51` | the single SubmitTask function |
| `service/consumer/distribute_tasks.go:15` | the task-type switch (all 7 types, one is dead) |
| `service/consumer/k8s_handler.go` | workflow chain implementation |
| `service/consumer/fault_injection.go:42` | batchManager and the legacy/guided branch |
| `service/producer/injection.go:806` | produce restart-pedestal tasks (where inject batches enter the async layer) |
| `database/entity.go` | every DB table |
| `consts/consts.go` | every enum and Redis key pattern |
| `consts/system.go` | RBAC matrix (roles, actions, scopes, PermXxx rule constants) |
