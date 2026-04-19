# 04 — Async / Workflow Orchestration Layer

Slice owner: `service/consumer/`, `service/logreceiver/`, `service/initialization/`, `client/k8s/`.
All claims cite source `file:line`. Code-only — docs ignored.

---

## 1. Task queue topology

### 1.1 Redis keys

Key constants are duplicated in two files (literal-identical strings):
- `service/consumer/task.go:36-44`
- `repository/task.go:23-31`

| Key | Type | Purpose | Producers (write) | Consumers (read) |
|---|---|---|---|---|
| `task:delayed` | ZSET (score = epoch sec exec time, member = JSON `UnifiedTask`) | Delayed queue | `repository.SubmitDelayedTask` (`repository/task.go:69-79`); rescore in `repository.ExpediteDelayedTask` (`repository/task.go:248-288`); re-add cron in `service/consumer/task.go:189` | `repository.ProcessDelayedTasks` Lua ZRANGEBYSCORE+LPUSH (`repository/task.go:82-110`); listing `repository.ListDelayedTasks` (`repository/task.go:150-166`); `RemoveFromZSet` for cancel (`repository/task.go:208`) |
| `task:ready` | LIST (FIFO via LPUSH/BRPOP, member = JSON `UnifiedTask`) | Ready queue | `repository.SubmitImmediateTask` LPUSH (`repository/task.go:36-43`); scheduler Lua LPUSH from delayed (`repository/task.go:90`) | `repository.GetTask` BRPOP (`repository/task.go:46-54`) called from `consumer.ConsumeTasks` (`service/consumer/task.go:221`); `RemoveFromList` cancel (`repository/task.go:174-205`); list snapshot via `ListReadyTasks` (`repository/task.go:169-171`) |
| `task:dead` | ZSET (score = retry-after epoch sec) | Dead-letter queue | `repository.HandleFailedTask` (`repository/task.go:57-64`) called from `consumer.handleFinalFailure` (`service/consumer/task.go:405`); `repository.HandleCronRescheduleFailure` (`repository/task.go:113-118`) | Cancel removal: `RemoveFromZSet` (`service/consumer/task.go:443-446`) |
| `task:index` | HASH `taskID -> queueKey` | Locate queue holding a task | `SubmitImmediateTask`, `SubmitDelayedTask`, scheduler Lua, `ExpediteDelayedTask` | `repository.GetTaskQueue` (`repository/task.go:145-147`); `repository.DeleteTaskIndex` (`repository/task.go:233`) |
| `task:concurrency_lock` | STRING (counter, INCR/DECR) | Global concurrency cap (`MaxConcurrency=20`) | `repository.AcquireConcurrencyLock` INCR (`repository/task.go:121-128`); `InitConcurrencyLock` SET 0 (`repository/task.go:131-134`) | `repository.ReleaseConcurrencyLock` DECR (`repository/task.go:137-142`) |
| `last_batch_info` | (declared but unused) | Constant only; no read/write found | `repository/task.go:29` declaration | — |
| `monitor:namespaces` | SET of namespace strings | Master list of monitored ns | `monitor.addNamespace` SADD (`service/consumer/monitor.go:455`) | `monitor.GetNamespaceToRestart` SMEMBERS (`service/consumer/monitor.go:266`); `RefreshNamespaces` (`monitor.go:361`); `InitializeNamespaces` (`monitor.go:315`) |
| `monitor:ns:%s` | HASH `{end_time, trace_id, status}` | Per-namespace lock | `monitor.AcquireLock` HSet via WATCH/Tx (`monitor.go:155-160`); `addNamespace` HSetNX (`monitor.go:456-458`); `setNamespaceStatus` (`monitor.go:517`); `ReleaseLock` HSet to "" (`monitor.go:238-242`) | `getNamespaceStatus` HGET (`monitor.go:497`); `isNamespaceLocked` HGET (`monitor.go:469-481`); `AcquireLock` read-side HGET (`monitor.go:133-147`) |
| `token_bucket:restart_service` | SET of taskIDs | Rate-limit pedestal restarts (max 2) | Lua SADD in `TokenBucketRateLimiter.AcquireToken` (`service/consumer/rate_limiter.go:78-99`) | `ReleaseToken` SREM (`rate_limiter.go:120-127`); also reads SCARD inside Lua |
| `token_bucket:build_container` | SET of taskIDs | Rate-limit container builds (max 3) | same Lua via `GetBuildContainerRateLimiter()` (`rate_limiter.go:195-198`) | same |
| `token_bucket:algo_execution` | SET of taskIDs | Rate-limit algorithm runs (max 5) | same via `GetAlgoExecutionRateLimiter()` (`rate_limiter.go:201-204`) | same |
| `injection:algorithms` | HASH `groupID -> JSON []ContainerVersionItem` | Per-group algorithm fan-out list (read by detector collector) | producer side (not in slice) | `client.CheckCachedField` + `client.GetHashField` in `executeCollectResult` (`service/consumer/collect_result.go:79-86`) |
| `trace:%s:log` (per-trace stream) | STREAM | All trace events (task lifecycle, lock, job, fault) | `publishEvent` -> `client.RedisXAdd` (`service/consumer/task.go:481`); 30+ call-sites inc. `monitor.AcquireLock`/`ReleaseLock` (`monitor.go:79,183`), `dispatchTask` (`distribute_tasks.go:26`), `k8s_handler.HandleJobFailed/Succeeded` (`k8s_handler.go:409,518`), `restart_pedestal.go:140,152`, `task.go:353,506` | producer SSE (out of slice) |
| `group:%s:log` | STREAM | Group-level terminal events for SSE | `publishGroupStreamEvent` `client.RedisXAdd` (`service/consumer/trace.go:514`) only when trace reaches Completed/Failed | producer SSE |
| `notifications:global` | STREAM | Global notifications | constant only in slice (`consts/consts.go:359`) | — |
| `joblogs:<task_id>` | Pub/Sub channel | OTLP log fan-out keyed by task_id | `OTLPLogReceiver.publishLogEntry` -> `client.RedisPublish` (`service/logreceiver/receiver.go:246-249`) | producer SSE (`service/producer/task.go:236` subscribes `joblogs:<taskID>`) |
| `config:updates:response` | Pub/Sub | Config update ack | `common.PublishWrapper` (`service/common/config_registry.go:84`) | producer side |

### 1.2 Entry points (call chains)

**`consumer.ConsumeTasks(ctx)`** — `service/consumer/task.go:207-234`
1. Loop: `repository.AcquireConcurrencyLock` (INCR `task:concurrency_lock`).
2. `repository.GetTask` BRPOP `task:ready` 30s.
3. `go processTask(ctx, taskData)` — releases lock in defer, unmarshals JSON, builds trace+task spans via `extractContext` (`task.go:276-308`), then `executeTaskWithRetry` (`task.go:311-377`).
4. `executeTaskWithRetry` registers cancel in `taskCancelFuncs`, loops up to `RetryPolicy.MaxAttempts` calling `dispatchTask` (`distribute_tasks.go:15-56`); on final failure -> `handleFinalFailure` (ZADD `task:dead`) and `updateTaskState(TaskError)`.

**`consumer.StartScheduler(ctx)`** — `service/consumer/task.go:142-154`
1. 1s ticker -> `processDelayedTasks` (`task.go:157-200`).
2. `repository.ProcessDelayedTasks` Lua atomically moves due tasks from `task:delayed` to `task:ready` and updates `task:index`.
3. For cron tasks (`task.CronExpr != ""`): compute next time via `common.CronNextTime`, re-marshal, `repository.SubmitDelayedTask`, `common.EmitTaskScheduled`.

**`common.SubmitTask(ctx, task)`** — `service/common/task.go:51-139` (called by consumer + producer)
1. Allocate UUID for missing TraceID/TaskID.
2. If parent exists: `repository.GetParentTaskLevelByID` to set `Level = parent+1`.
3. If non-immediate: `calculateExecuteTime` (cron only).
4. If root task (no parent, not rescheduled): build a `Trace` via `t.ConvertToTrace` and `repository.UpsertTrace`.
5. `repository.UpsertTask` to MySQL.
6. JSON-marshal `t`, then either `repository.SubmitImmediateTask` (LPUSH `task:ready`) or `repository.SubmitDelayedTask` (ZADD `task:delayed`) + `EmitTaskScheduled` event.

---

## 2. Task type inventory

Task type enum: `consts/consts.go:275-283`. Names: `consts/collection.go:130-136`. Validation set: `consts/validation.go:203-209`.

| Type | Handler in consumer | Next task(s) on success |
|---|---|---|
| `TaskTypeBuildContainer` (0) | `executeBuildContainer` (`build_container.go:41-107`) — BuildKit build+push | none (terminal) |
| `TaskTypeRestartPedestal` (1) | `executeRestartPedestal` (`restart_pedestal.go:31-186`) — Helm install pedestal in locked ns | `common.ProduceFaultInjectionTasks` -> `TaskTypeFaultInjection` (`restart_pedestal.go:179`) |
| `TaskTypeFaultInjection` (2) | `executeFaultInjection` (`fault_injection.go:111-301`) — `chaos.BatchCreate` Chaos Mesh CRDs | submitted **by K8s callback** `HandleCRDSucceeded` -> `TaskTypeBuildDatapack` (`k8s_handler.go:288-301`) |
| `TaskTypeRunAlgorithm` (3) | `executeAlgorithm` (`algo_execution.go:62-145`) — creates K8s Job | submitted by `HandleJobSucceeded` -> `TaskTypeCollectResult` (`k8s_handler.go:652-665`) |
| `TaskTypeBuildDatapack` (4) | `executeBuildDatapack` (`build_datapack.go:55-95`) — creates K8s Job | submitted by `HandleJobSucceeded` -> `TaskTypeRunAlgorithm` (detector) (`k8s_handler.go:580-593`) |
| `TaskTypeCollectResult` (5) | `executeCollectResult` (`collect_result.go:27-127`) — reads results from DB; if detector with anomalies, fans out RCA algos | `produceAlgorithmExeuctionTask` -> `TaskTypeRunAlgorithm` per algo in `injection:algorithms` (`collect_result.go:155-174`) |
| `TaskTypeCronJob` (6) | **Not in switch** in `dispatchTask` — handled only by scheduler reschedule (`task.go:172-198`) | reschedules itself |

Default branch returns `unknown task type: %d` (`distribute_tasks.go:48`).

---

## 3. Consumer file -> purpose table

| File | Purpose | Exported symbols | Task types handled |
|---|---|---|---|
| `task.go` | Redis queue consumer loop, scheduler, retry, cancel registry, event publishing, state updates | `StartScheduler`, `ConsumeTasks`, `CancelTask`, queue-key consts (`DelayedQueueKey`...,`MaxConcurrency`) | all (via dispatch) |
| `distribute_tasks.go` | `dispatchTask` switch on `task.Type` | none exported | all |
| `common.go` | Shared helpers `getRequiredVolumeMountConfigs`, `handleExecutionError` (consumer-private) | none exported | — |
| `build_container.go` | BuildKit image build + push (`buildImageAndPush`), reschedule on token starvation | none exported | `TaskTypeBuildContainer` |
| `restart_pedestal.go` | Helm install pedestal, then submit fault injection task | none exported | `TaskTypeRestartPedestal` |
| `fault_injection.go` | Convert `Node`/`GuidedConfig` -> `InjectionConf`, `chaos.BatchCreate`, persist `FaultInjection`, batchManager | none exported | `TaskTypeFaultInjection` |
| `build_datapack.go` | Build K8s Job for benchmark container datapack collection | none exported | `TaskTypeBuildDatapack` |
| `algo_execution.go` | Build K8s Job for algorithm container | none exported | `TaskTypeRunAlgorithm` |
| `collect_result.go` | Read execution/detector results from DB, branch on anomaly, fan out RCA algos | none exported | `TaskTypeCollectResult` |
| `k8s_handler.go` | Implements `k8s.Callback` (CRD/Job lifecycle); parses labels/annotations; submits next-stage tasks | `NewHandler` | reacts to all (CRD: FaultInjection; Job: BuildDatapack, RunAlgorithm) |
| `monitor.go` | Namespace lock manager via Redis | `GetMonitor`, `MonitorItem`, `LockMessage`, `NamespaceRefreshResult`, `NamespaceInitResult` | — |
| `rate_limiter.go` | Token-bucket rate limiters for restart/build/algo | `TokenBucketRateLimiter`, `RateLimiterConfig`, `GetRestartPedestalRateLimiter`, `GetBuildContainerRateLimiter`, `GetAlgoExecutionRateLimiter` | — |
| `config_handlers.go` | Etcd config-update handlers (chaos system count, rate limits) | `RegisterConsumerHandlers`, `UpdateK8sController` | — |
| `trace.go` | Infer trace state from task tree, optimistic-lock update of `Trace` row, group SSE | none exported | — |
| `jvm_runtime_mutator.go` | Entire file is a Go block-comment (dead) | — | — |

---

## 4. K8s controller

### 4.1 `Callback` interface — `client/k8s/controller.go:46-54`
```go
HandleCRDAdd(name, annotations, labels)
HandleCRDDelete(namespace, annotations, labels)
HandleCRDFailed(name, annotations, labels, errMsg)
HandleCRDSucceeded(namespace, pod, name, startTime, endTime, annotations, labels)
HandleJobAdd(name, annotations, labels)
HandleJobFailed(job, annotations, labels)
HandleJobSucceeded(job, annotations, labels)
```
Implemented by `*k8sHandler` in `service/consumer/k8s_handler.go`.

### 4.2 Watched CRDs (GVKs)
Loaded via `chaosCli.GetCRDMapping()` (`controller.go:102-105`). Source: `chaos-experiment/client/kubernetes.go:235-245`.
- `chaos-mesh.org/v1alpha1 DNSChaos`
- `chaos-mesh.org/v1alpha1 HTTPChaos`
- `chaos-mesh.org/v1alpha1 JVMChaos`
- `chaos-mesh.org/v1alpha1 NetworkChaos`
- `chaos-mesh.org/v1alpha1 PodChaos`
- `chaos-mesh.org/v1alpha1 StressChaos`
- `chaos-mesh.org/v1alpha1 TimeChaos`

Plus platform-namespace `Job` and `Pod` (filtered by label `rcabench_app_id=<AppID>`) — `controller.go:88-100`.

### 4.3 Handler implementations + next submissions

| Callback | File:line | What it submits next |
|---|---|---|
| `HandleCRDAdd` | `k8s_handler.go:133-156` | none — only emits `EventFaultInjectionStarted` and updates state to Running |
| `HandleCRDDelete` | `k8s_handler.go:158-175` | none — `monitor.ReleaseLock` |
| `HandleCRDFailed` | `k8s_handler.go:177-228` | none — sets injection state Failed; if hybrid, increments batch counter |
| `HandleCRDSucceeded` | `k8s_handler.go:230-317` | **Submits `TaskTypeBuildDatapack`** via `common.SubmitTask` (lines 288-302). For hybrid batches, defers until all CRDs in batch report. |
| `HandleJobAdd` | `k8s_handler.go:319-364` | none — Running state + start event by task type |
| `HandleJobFailed` | `k8s_handler.go:366-485` | none — releases algo rate-limit token; updates Execution to Failed; emits `EventDatapackBuildFailed`/`EventAlgoRunFailed` |
| `HandleJobSucceeded` | `k8s_handler.go:487-668` | If task type was `BuildDatapack`: **submits `TaskTypeRunAlgorithm`** for detector (`config.GetDetectorName()`) (lines 580-593). If `RunAlgorithm`: **submits `TaskTypeCollectResult`** (lines 652-665). Releases algo rate-limit token. |

CRD recovery is driven from `Controller.checkRecoveryStatus` -> `handleCRDSuccess` -> `Callback.HandleCRDSucceeded` (`controller.go:622-689`), which is gated by detection of phase transition `Run -> Stop` and `AllInjected` condition (`controller.go:380-441`); `CheckRecovery` queue-item is enqueued with `AddAfter(duration)`.

CRD/Job cleanup goes through `processQueueItem` `DeleteCRD`/`DeleteJob` (`controller.go:586-619`) -> `client/k8s/crd.go:deleteCRD`, `client/k8s/job.go:deleteJob`. Skipped when `debugging.enabled=true`.

### 4.4 batchManager — `service/consumer/fault_injection.go:42-97`

State (in-memory singleton, `getBatchManager`):
- `batchCounts map[batchID]int` — completion count
- `batchInjections map[batchID][]string` — names of CRDs in this batch

Triggered when:
- Set up: `setBatchInjections(batchID, names)` after `chaos.BatchCreate` returns `>1` names (`fault_injection.go:247`).
- Increment: `HandleCRDSucceeded`/`HandleCRDFailed` call `incrementBatchCount` when `IsHybrid` label is true (`k8s_handler.go:220, 310`).
- Fire: `isFinished(batchID)` returns true when count reaches `len(injectionNames)` -> `deleteBatch` and run `postProcess(batchID)` (single state update + child task submission for the whole batch).

Carries via labels:
- `batch_id` (`consts.CRDLabelBatchID`) added to CRD labels at `fault_injection.go:230`.
- `is_hybrid` (`consts.CRDLabelIsHybrid`) literal `"true"`/`"false"` at `fault_injection.go:231`. Hybrid when `len(nodes) > 1 || len(guidedConfigs) > 1` (`fault_injection.go:225`).

---

## 5. Workflow chain (plain text)

Trigger origins: producer-side `SubmitTask` for root task (out of slice). Then:

```
[ROOT] common.SubmitTask  (service/common/task.go:51)
   |  LPUSH task:ready  (or ZADD task:delayed)
   v
ConsumeTasks BRPOP task:ready  (service/consumer/task.go:221)
   v
processTask -> executeTaskWithRetry -> dispatchTask switch  (distribute_tasks.go:34)
   |
   +-- TaskTypeRestartPedestal -> executeRestartPedestal (restart_pedestal.go:31)
   |       |  monitor.GetNamespaceToRestart  (acquires monitor:ns:<ns>)
   |       |  installPedestal via Helm
   |       |  common.ProduceFaultInjectionTasks  (restart_pedestal.go:179)
   |       v
   +-- TaskTypeFaultInjection -> executeFaultInjection (fault_injection.go:111)
           |  monitor.CheckNamespaceToInject
           |  chaos.BatchCreate -> creates Chaos Mesh CRD(s) carrying:
           |     - annotations: task_carrier, trace_carrier, benchmark
           |     - labels: rcabench_app_id, task_id, trace_id, group_id,
           |               project_id, user_id, task_type, batch_id, is_hybrid
           |  Persists FaultInjection row
           v
        Chaos Mesh runs the injection
           v
   k8s.Controller CRD informer detects Run->Stop transition (controller.go:389)
           v
   AllInjected condition -> queue.AddAfter(CheckRecovery, duration)  (controller.go:430)
           v
   processQueueItem CheckRecovery -> AllRecovered? -> handleCRDSuccess
           v
   Callback.HandleCRDSucceeded  (k8s_handler.go:230)
           |  updates injection state: DatapackInjectSuccess
           |  builds payload {benchmark, datapack, dataset_version_id}
           |  common.SubmitTask(TaskTypeBuildDatapack)  (k8s_handler.go:301)
           v
   ConsumeTasks picks up BuildDatapack
           v
   executeBuildDatapack (build_datapack.go:55)
           |  k8s.CreateJob  (annotations include datapack JSON,
           |                  labels include datapack name + dataset_id)
           v
   Job runs in k8s.namespace (config.GetString("k8s.namespace"))
           v
   Controller jobInformer UpdateFunc: Succeeded transition (controller.go:491)
           v
   Callback.HandleJobSucceeded  (k8s_handler.go:487)
           |  task type == BuildDatapack branch (k8s_handler.go:529-594):
           |    updateInjectionState(DatapackBuildSuccess)
           |    map detector container ref -> ContainerVersion
           |    build payload {algorithm: detector, datapack, dataset_version_id}
           |    common.SubmitTask(TaskTypeRunAlgorithm)  (k8s_handler.go:592)
           v
   executeAlgorithm (algo_execution.go:62)
           |  GetAlgoExecutionRateLimiter (token_bucket:algo_execution)
           |  createExecution row in DB
           |  k8s.CreateJob with init container that mkdir OUTPUT_PATH
           v
   Job completes -> Controller -> HandleJobSucceeded RunAlgorithm branch (k8s_handler.go:596-667)
           |  rateLimiter.ReleaseToken
           |  if algorithm == detector: updateInjectionState(DatapackDetectorSuccess)
           |  updateExecutionState(ExecutionSuccess)
           |  common.SubmitTask(TaskTypeCollectResult)  (k8s_handler.go:664)
           v
   executeCollectResult (collect_result.go:27)
           |  if detector:
           |     repository.ListDetectorResultsByExecutionID
           |     event = EventDatapackResultCollection / EventDatapackNoAnomaly /
           |             EventDatapackNoDetectorData
           |     if hasIssues && client.CheckCachedField(injection:algorithms, groupID):
           |        for each algo in HASH injection:algorithms[groupID]:
           |           produceAlgorithmExeuctionTask -> common.SubmitTask(TaskTypeRunAlgorithm)
           |  else (RCA algo result):
           |     repository.ListGranularityResultsByExecutionID
           |     event = EventAlgoResultCollection / EventAlgoNoResultData
           v
   updateTaskState -> updateTraceState (trace.go:84)
           |  inferTraceState -> Completed (or Failed) when leaf level satisfied
           |  publishGroupStreamEvent on group:%s:log when terminal
```

Special branches:
- `BuildContainer` is a stand-alone leaf — no callback chain.
- Rate-limit starvation: `executeBuildContainer` / `executeAlgorithm` / `executeRestartPedestal` call `rescheduleXXXTask` which calls `task.Reschedule(executeTime)` then `common.SubmitTask` (delayed); state moves to `TaskRescheduled` (no `EventNoTokenAvailable` /`EventNoNamespaceAvailable`).
- Pod `ImagePullBackOff` triggers `Callback.HandleJobFailed` indirectly via `genPodEventHandlerFuncs` (`controller.go:519-558`).

---

## 6. Label / annotation schema on K8s resources

All values in `consts/consts.go:452-479`.

### Annotations (CRDs and Jobs)

| Key | Where applied | Value |
|---|---|---|
| `task_carrier` | both | OTel propagator `MapCarrier` for task span (`dto/task.go:104`, ann produced by `t.GetAnnotations`) |
| `trace_carrier` | both | OTel propagator `MapCarrier` for trace span (`dto/task.go:112`) |
| `benchmark` (`CRDAnnotationBenchmark`) | CRD only | JSON `dto.ContainerVersionItem` of benchmark; set in `executeFaultInjection` (`fault_injection.go:222`) |
| `algorithm` (`JobAnnotationAlgorithm`) | Job (RunAlgorithm) | JSON `dto.ContainerVersionItem` (`algo_execution.go:113`) |
| `datapack` (`JobAnnotationDatapack`) | Job (RunAlgorithm + BuildDatapack) | JSON `dto.InjectionItem` (`algo_execution.go:119`, `build_datapack.go:74`) |

### Labels

| Key | Where applied | Value |
|---|---|---|
| `rcabench_app_id` (`K8sLabelAppID`) | CRD + Job | global `consts.AppID` (a ULID set at consumer startup `main.go:124`); used by informer LabelSelector (`controller.go:85, 147`) |
| `task_id` (`JobLabelTaskID`) | both | UnifiedTask.TaskID |
| `task_type` (`JobLabelTaskType`) | both | name from `consts.GetTaskTypeName` |
| `trace_id` (`JobLabelTraceID`) | both | UnifiedTask.TraceID |
| `group_id` (`JobLabelGroupID`) | both | UnifiedTask.GroupID |
| `project_id` (`JobLabelProjectID`) | both | itoa(ProjectID) |
| `user_id` (`JobLabelUserID`) | both | itoa(UserID) |
| `batch_id` (`CRDLabelBatchID`) | CRD only | `batch-<ULID>` (`fault_injection.go:224, 230`) |
| `is_hybrid` (`CRDLabelIsHybrid`) | CRD only | "true"/"false" (`fault_injection.go:225, 231`) |
| `datapack` (`JobLabelDatapack`) | Job (datapack + algo) | `payload.datapack.Name` (`build_datapack.go:80`, `algo_execution.go:125`) |
| `dataset_id` (`JobLabelDatasetID`) | Job (datapack) | itoa of `datasetVersionID` or 0 (`build_datapack.go:81`) |
| `execution_id` (`JobLabelExecutionID`) | Job (algo) | itoa(executionID) (`algo_execution.go:126`) |
| `timestamp` (`JobLabelTimestamp`) | Job (algo) — appended at runtime | algo OUTPUT_PATH timestamp set at `algo_execution.go:249` |
| `job-name` (`JobLabelName`) | Job | `JobConfig.JobName` (set in `k8s.CreateJob` at `client/k8s/job.go:134`) |

---

## 7. OTLP log receiver — `service/logreceiver/`

- HTTP server (port `consts.DefaultPort = 4319`, override `otlp_receiver.port` in config) — `receiver.go:26-29, 51-101`. Started from `main.go:140-150` and `184-192` in consumer/both modes.
- Endpoints: `POST /v1/logs`, `GET /health` (`receiver.go:68-70`). Body size cap 5MB (`receiver.go:31`).
- Accepts `application/x-protobuf` (`proto.Unmarshal` into `collogspb.ExportLogsServiceRequest`) and `application/json` (custom parse via `parseJSONRequest` -> `parseResourceLog`, `receiver.go:216-352`); supports `Content-Encoding: gzip` (`receiver.go:144-161`).
- `parseOTLPLogs` walks ResourceLogs -> ScopeLogs -> LogRecords (`parser.go:18-36`); each LogRecord -> `dto.LogEntry{Timestamp, Line, TaskID, TraceID, JobID, Level}` with attribute precedence log > resource (`parser.go:39-53`).
- Output: `publishLogEntry` Redis Pub/Sub `joblogs:<TaskID>` (`receiver.go:246-249`). Empty `task_id` entries are dropped (`parser.go:28`).
- Downstream consumer: producer SSE handler subscribes `joblogs:<taskID>` (`service/producer/task.go:236`, outside slice).
- Counters via `atomic.Int64`: `received_total`, `published_total`, `errors_total` (visible at `/health`).

---

## 8. `initialization/` package

Per-mode init orchestration. `main.go` invokes:

| Caller | Order |
|---|---|
| `producer` mode (`main.go:97-115`) | `database.InitDB` -> `initialization.InitializeProducer(ctx)` -> `utils.InitValidator` -> `client.InitTraceProvider` -> `initChaosExperiment` -> `router.New` |
| `consumer` mode (`main.go:118-155`) | sets `consts.InitialTime`, `consts.AppID`; `initChaosExperiment`; spawn `k8s.GetK8sController().Initialize(..., consumer.NewHandler())`; `database.InitDB`; `initialization.InitializeConsumer(ctx)`; `initialization.InitConcurrencyLock(ctx)`; `client.InitTraceProvider`; `runRateLimiterStartupGC`; spawn OTLP receiver; spawn `consumer.StartScheduler`; `consumer.ConsumeTasks` (blocks) |
| `both` mode (`main.go:158-203`) | same as consumer, but inserts `InitializeProducer` before `InitializeConsumer`, plus router goroutine |

`InitializeProducer` (`producer.go:37-54`):
1. `newConfigData(ConfigScopeProducer)` reads existing dynamic configs from DB.
2. If empty: `initializeProducer` seeds resources, system roles, permissions, role-perm bindings, admin user + super_admin role, projects, teams, users, containers (via `producer.CreateContainerCore` and Helm value upload for pedestals), datasets, execution labels — all under `withOptimizedDBSettings` (FK_CHECKS=0) (`utils.go:38-56`).
3. `InitializeSystems()` — `systems.go:27-67`: sets `config.SetChaosConfigDB(database.DB)`, seeds 6 builtin systems (train-ticket, sock-shop, social-network, online-boutique, hotel-reservation, media-microsvc), registers each enabled system with `chaos.RegisterSystem`, and sets `chaos.SetMetadataStore(common.NewDBMetadataStore())`.
4. `registerHandlers(ctx, ConfigScopeProducer, nil)` — `common.go:12-38`: registers global handlers (`common.RegisterGlobalHandlers`) + activates etcd listener for global scope, then syncs detector name from viper.

`InitializeConsumer` (`consumer.go:28-66`):
1. `newConfigData(ConfigScopeConsumer)`.
2. If empty: seeds dynamic configs from initial data file.
3. `registerHandlers(ctx, ConfigScopeConsumer, consumer.RegisterConsumerHandlers)` — also activates consumer-scope etcd listener; `RegisterConsumerHandlers` registers `chaosSystemCountHandler` (category `injection.system.count`) and `rateLimitingConfigHandler` (category `rate_limiting`) (`consumer/config_handlers.go:18-27`).
4. `monitor.InitializeNamespaces` -> bootstraps `monitor:namespaces` SET and per-ns hashes; then `consumer.UpdateK8sController` adds informers for the active namespaces.

`InitConcurrencyLock(ctx)` — `consumer.go:22-26`: SET `task:concurrency_lock = 0`.

Dependency order matters: K8s controller goroutine starts BEFORE `database.InitDB` in consumer mode (`main.go:128-130`); etcd config listener started inside `registerHandlers` after DB init.

---

## 9. Consumer outbound edges (deduped)

### -> `service/common`
- `SubmitTask`, `ProduceFaultInjectionTasks`, `EmitTaskScheduled`, `CronNextTime`, `MapRefsToContainerVersions`, `CreateOrUpdateLabelsFromItems`, `RegisterHandler`, `RegisterGlobalHandlers`, `ListRegisteredConfigKeys`, `PublishWrapper`

### -> `repository`
- `ProcessDelayedTasks`, `SubmitDelayedTask`, `SubmitImmediateTask` (via common), `HandleCronRescheduleFailure`, `HandleFailedTask`, `AcquireConcurrencyLock`, `ReleaseConcurrencyLock`, `InitConcurrencyLock` (via initialization), `GetTask`, `GetTaskQueue`, `RemoveFromList`, `RemoveFromZSet`, `DeleteTaskIndex`, `UpdateTaskState`, `GetParentTaskLevelByID` (via common), `GetTraceByID`, `GetExecutionByID`, `UpdateExecution`, `GetInjectionByName`, `GetInjectionByID`, `UpdateInjection`, `CreateInjection`, `AddInjectionLabels`, `CreateExecution`, `AddExecutionLabels`, `ListDetectorResultsByExecutionID`, `ListGranularityResultsByExecutionID`

### -> `database`
- `database.DB` (gorm `*DB`), transactions; `database.Trace`, `database.Task`, `database.Execution`, `database.FaultInjection`, `database.Groundtruth`, `database.NewDBGroundtruth`, `database.NewDatabaseConfig`, `database.DatabaseConfig`

### -> `client`
- `client.GetRedisClient`, `client.RedisXAdd`, `client.RedisPublish` (logreceiver only), `client.GetRedisListRange`, `client.GetRedisZRangeByScoreWithScores`, `client.CheckCachedField`, `client.GetHashField`, `client.NewHelmClient`

### -> `client/k8s`
- `k8s.CreateJob`, `k8s.GetJobPodLogs`, `k8s.GetK8sController`, `k8s.Controller` (`AddNamespaceInformers`, `RemoveNamespaceInformers`), `k8s.GetVolumeMountConfigMap`, `k8s.VolumeMountConfig`, `k8s.JobConfig`

### -> `tracing`
- `tracing.WithSpan`, `tracing.SetSpanAttribute`

### -> `utils`
- `utils.ConvertToType[T]`, `utils.MergeSimpleMaps`, `utils.GenerateULID`, `utils.MakeSet`, `utils.GenerateServiceToken`, `utils.GetCallerInfo`, `utils.StringPtr`, `utils.IntPtr`, `utils.GetIntValue`, `utils.GetPointerIntFromMap`, `utils.TimePtr` (init only)

### -> `dto`
- `UnifiedTask` (+ methods `GetTraceCtx`, `GetGroupCtx`, `SetTraceCtx`, `SetGroupCtx`, `GetAnnotations`, `GetLabels`, `Reschedule`, `ConvertToTask`, `ConvertToTrace`); `TraceStreamEvent`, `GroupStreamEvent`, `LogEntry`, `JobMessage`, `DatapackInfo`, `DatapackResult`, `ExecutionInfo`, `ExecutionResult`, `InfoPayloadTemplate`, `ContainerVersionItem`, `InjectionItem`, `LabelItem`, `ParameterItem`, `BuildOptions`, `HelmConfigItem`, `ContainerRef`, `NewContainerVersionItem`, `NewInjectionItem`, `TaskScheduledReasonCronNext`, `TaskScheduledReasonPreDurationWait`, `TaskScheduledPayload`

### -> `config`
- `config.GetString`, `config.GetInt`, `config.GetBool`, `config.GetMap` (k8s), `config.GetDetectorName`, `config.SetDetectorName` (init), `config.GetChaosSystemConfigManager`, `config.SetChaosConfigDB` (init), `config.GetAllNamespaces`

### -> `chaos-experiment` (`github.com/OperationsPAI/chaos-experiment/...`)
- `chaos.SystemType`, `chaos.ChaosType`, `chaos.ChaosNameMap`, `chaos.Node`, `chaos.NodeToStruct`, `chaos.InjectionConf`, `chaos.BatchCreate`, `chaos.RegisterSystem`, `chaos.SetMetadataStore`, `chaos.SystemConfig` (init); `guidedcli.GuidedConfig`, `guidedcli.BuildInjection`; `chaosCli.GetCRDMapping`, `chaosCli.InitWithConfig`

---

## 10. Surprises / dead code

1. `service/consumer/jvm_runtime_mutator.go` — entire file is one big `/* ... */` Go-block comment referring to a removed `Consumer`/`task.Status` model (~100 lines, builds clean but contributes nothing).
2. `LastBatchInfoKey = "last_batch_info"` is declared in both `service/consumer/task.go:42` and `repository/task.go:29` but never read or written anywhere in the slice.
3. The Redis-key constants are defined twice (once in `service/consumer/task.go`, once in `repository/task.go`) with identical literals; only the `repository` ones are functionally used. The `consumer.MaxConcurrency` constant is a duplicate of `repository.MaxConcurrency` and is only referenced via the repository copy.
4. `TaskTypeCronJob` is declared (`consts/consts.go:282`) and accepted by `consts.ValidTaskTypes`, but `dispatchTask` has no case for it — only the scheduler `processDelayedTasks` knows how to handle the cron-rescheduling path.
5. `TaskCancelled = -2` exists (`consts/consts.go:243`) but is never set or transitioned to from any code in this slice; `CancelTask` only deletes the task from queues and aborts in-flight context.
6. `executeTaskWithRetry` creates a per-attempt `context.WithCancel` (`task.go:331`) and immediately discards `cancel` via `_ = cancel` — leaking a cancel func per retry attempt.
7. `RemoveFromZSet`'s log-line on failure (`service/consumer/task.go:441, 445`) prints `err` from outer scope which is from `GetTaskQueue` and may be nil at that point; the logic is also wrong-shadowed.
8. `getEventTypeByTask` returns the literal string `"unknown"` for `TaskTypeCollectResult` (`trace.go:60`) — collect-result events are then patched in by `tryUpdateTraceStateCore` (`trace.go:157-169`), which is a code-smell branch.
9. `OTLPLogReceiver.publishLogEntry` writes to a Redis Pub/Sub channel (`joblogs:<taskID>`), not a Redis Stream — so logs are dropped if no subscriber is online; the receiver's `Shutdown()` is `defer`red inside the goroutine after `Start` blocks, so it never actually fires (`main.go:140-150`).
10. `installPedestal` retries `helmClient.Install` from `LocalPath` after a remote failure, but the second `Install` call passes `item.Version` (which may be a remote-only chart version) to a local-path install (`restart_pedestal.go:346-353`) — likely silently inert, since helm-cli ignores `--version` for local paths.
