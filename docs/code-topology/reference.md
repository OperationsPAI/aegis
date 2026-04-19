# AegisLab Data-Shape Reference

Quick-lookup tables for the pieces that matter when tracing flows. Cross-ref `slices/05-data.md` for exhaustive field lists.

## 1. Entity FK graph (MySQL; 40 tables)

All entities use custom soft-delete via `Status consts.StatusType` (`-1=deleted, 0=disabled, 1=enabled`). **No `gorm.DeletedAt`** is used. Many entities expose a `Active*` virtual column + unique index so uniqueness only holds over non-deleted rows.

```
User ───(updated_by)── DynamicConfig ──(config_id)── ConfigHistory
  │                       │                           │(operator_id)
  │                       └──M2M(config_labels)── Label
  │                                                 ▲
  ├──────────┬──────┬──────┬──────┬─────┐           │
  │          │      │      │      │     │           │ M2M:
  │          │      │      │      │     │           │  container_labels
  │          │      │      │      │     │           │  dataset_labels
  (user_id)  │      │      │      │     │           │  project_labels
  UserRole   UserContainer UserDataset  UserProject │  fault_injection_labels (FI CASCADE)
  UserTeam   UserPermission                         │  execution_injection_labels (Exec CASCADE)
  RolePermission                                    │
                                                    │
Container ──1:n── ContainerVersion ──1:1── HelmConfig
                                       └──M2M(container_version_env_vars)── ParameterConfig
                                       └──M2M(helm_config_values)─────────── ParameterConfig
Dataset   ──1:n── DatasetVersion   ──M2M(dataset_version_injections)── FaultInjection
Project   ──(team_id)── Team
Resource ──(parent_id)── Resource   (self-referential)
Permission──(resource_id)── Resource
AuditLog  ──(user_id, resource_id)── User, Resource

Trace ──1:n── Task
Task  ──(parent_task_id CASCADE self)── Task
FaultInjection ──(task_id CASCADE, benchmark_id SET NULL, pedestal_id SET NULL)── Task, ContainerVersion
Execution      ──(task_id CASCADE, algorithm_version_id RESTRICT, datapack_id RESTRICT,
                  dataset_version_id SET NULL)── Task, ContainerVersion, FaultInjection, DatasetVersion
DetectorResult     ──(execution_id CASCADE)── Execution
GranularityResult  ──(execution_id CASCADE)── Execution

System ──1:n(via name string)── SystemMetadata
Evaluation ──(project_id nullable)── Project
```

**Read-only SDK tables** (excluded from AutoMigrate — owned by Python SDK):
- `data` → `SDKDatasetSample` (`database/sdk_entities.go:7`)
- `evaluation_data` → `SDKEvaluationSample` (`database/sdk_entities.go:26`)

**SQL views** (recreated on every boot — `database/view.go:69 createDetectorViews`):
- `fault_injection_no_issues` → `FaultInjectionNoIssues`
- `fault_injection_with_issues` → `FaultInjectionWithIssues`

Both views hard-code `algorithm_id = 1` as the detector and LEFT JOIN the fault-injection labels. See `orphans.md`.

## 2. Redis key catalog

Full table (producer / consumer / who reads):

| Key pattern | Type | Purpose | Set by | Read by |
|---|---|---|---|---|
| `task:delayed` | ZSET (score=exec time) | delayed queue | `repository.SubmitDelayedTask`, `ExpediteDelayedTask`, scheduler Lua | `repository.ProcessDelayedTasks` (Lua) |
| `task:ready` | LIST (FIFO) | ready queue | `repository.SubmitImmediateTask`, scheduler Lua | `repository.GetTask` BRPOP in `consumer.ConsumeTasks` |
| `task:dead` | ZSET | dead letter (failures + cron-reschedule failures) | `repository.HandleFailedTask`, `HandleCronRescheduleFailure` | cancel path only |
| `task:index` | HASH `taskID→queueKey` | locate a task's current queue | all submit paths, scheduler | `repository.GetTaskQueue`, cancel paths |
| `task:concurrency_lock` | STRING counter | global cap 20 | `repository.AcquireConcurrencyLock`, `InitConcurrencyLock` | `ReleaseConcurrencyLock` |
| `last_batch_info` | — | **dead constant**, no reads/writes anywhere | — | — |
| `monitor:namespaces` | SET | set of monitored namespaces | `monitor.addNamespace`, `InitializeNamespaces`, `RefreshNamespaces` | `monitor.GetNamespaceToRestart` |
| `monitor:ns:<ns>` | HASH `{end_time, trace_id, status}` | per-namespace lock | `monitor.AcquireLock` (WATCH/tx), `ReleaseLock`, `setNamespaceStatus` | `AcquireLock`, `isNamespaceLocked`, `getNamespaceStatus` |
| `token_bucket:restart_service` | SET of taskIDs | pedestal restart semaphore (cap=2) | Lua SADD in `rate_limiter.AcquireToken` | Lua SCARD, `ReleaseToken`, `GCRateLimiters` |
| `token_bucket:build_container` | SET of taskIDs | container build semaphore (cap=3) | same | same |
| `token_bucket:algo_execution` | SET of taskIDs | algorithm run semaphore (cap=5) | same | same |
| `injection:algorithms` | HASH `groupID → JSON []ContainerVersionItem` | per-group algorithm fan-out list | `service/producer/injection.go` (SetHashField during `ProduceRestartPedestalTasks`) | `collect_result.go` detector branch fan-out |
| `trace:<traceID>:log` | STREAM | per-trace event ledger for SSE replay + live tail | ~30 call sites across consumer (every state transition) | `producer.NewStreamProcessor` for `/api/v2/traces/:id/stream` |
| `group:<groupID>:log` | STREAM | group-level terminal events | `trace.publishGroupStreamEvent` only at terminal | `/api/v2/groups/:id/stream` |
| `notifications:global` | STREAM | global notifications | (constant only in slice — no writers found in current code) | `/api/v2/notifications/stream` |
| `joblogs:<taskID>` | PUB/SUB channel | real-time job logs from OTLP receiver | `service/logreceiver.publishLogEntry` | `producer.TaskLogStreamer.StreamLogs` → WS handler |
| `config:updates:response` | PUB/SUB channel | config hot-reload ack | `common.PublishWrapper` in `config_registry.go` | producer-side waiters |
| `blacklist:token:*` | string keys | JWT blacklist (logout + revoke) | `repository/token.go` AddTokenToBlacklist | `IsTokenBlacklisted` SCAN (expensive — see orphans) |
| `rcabench:debug:history` | STREAM | debug-knob change audit | `client/debug/status_registry.go Set` | `.GetHistory` |
| `system:metrics:*` | ZSET | historical CPU/mem/disk samples | `producer/system.go StoreSystemMetrics` (1min init-goroutine) | `GetSystemMetricsHistory` |

## 3. Task types (enum `consts.TaskType`)

| Value | Name | Executor | Enqueued by | On success submits |
|---|---|---|---|---|
| 0 | BuildContainer | `executeBuildContainer` (build_container.go:41) | `producer.ProduceContainerBuildingTask` (HTTP `POST /containers/build`) | — (leaf) |
| 1 | RestartPedestal | `executeRestartPedestal` (restart_pedestal.go:31) | `producer.ProduceRestartPedestalTasks` | `TaskTypeFaultInjection` (via `common.ProduceFaultInjectionTasks`) |
| 2 | FaultInjection | `executeFaultInjection` (fault_injection.go:111) | `common.ProduceFaultInjectionTasks` | — (chain resumes on CRD callback) |
| 3 | RunAlgorithm | `executeAlgorithm` (algo_execution.go:62) | `producer.ProduceAlgorithmExeuctionTasks` [sic], `HandleJobSucceeded` BuildDatapack branch, `collect_result` detector fan-out | — (chain resumes on Job callback) |
| 4 | BuildDatapack | `executeBuildDatapack` (build_datapack.go:55) | `HandleCRDSucceeded` callback, `producer.ProduceDatapackBuildingTasks` (HTTP `POST /injections/build`) | — (chain resumes on Job callback) |
| 5 | CollectResult | `executeCollectResult` (collect_result.go:27) | `HandleJobSucceeded` RunAlgorithm branch | optionally fans out more `TaskTypeRunAlgorithm` tasks |
| 6 | CronJob | **not in dispatch switch** | scheduler reschedule path only | rescheduled by scheduler |

## 4. K8s label / annotation schema

All keys live in `consts/consts.go:452-479`. Applied by `dto.UnifiedTask.GetLabels()` / `GetAnnotations()`, augmented in `fault_injection.go`, `build_datapack.go`, `algo_execution.go`.

### Annotations

| Key | On CRD | On Job | Value |
|---|---|---|---|
| `task_carrier` | ✓ | ✓ | OTel propagator MapCarrier for task span |
| `trace_carrier` | ✓ | ✓ | OTel propagator MapCarrier for trace span |
| `benchmark` | ✓ | — | JSON `dto.ContainerVersionItem` of benchmark (fault_injection.go:222) |
| `algorithm` | — | ✓ (RunAlgorithm) | JSON `dto.ContainerVersionItem` (algo_execution.go:113) |
| `datapack` | — | ✓ (RunAlgorithm + BuildDatapack) | JSON `dto.InjectionItem` |

### Labels

| Key | On CRD | On Job | Value |
|---|---|---|---|
| `rcabench_app_id` | ✓ | ✓ | global `consts.AppID` ULID (set once at consumer start `main.go:124`); **informer LabelSelector** |
| `task_id` | ✓ | ✓ | UnifiedTask.TaskID |
| `task_type` | ✓ | ✓ | `consts.GetTaskTypeName` |
| `trace_id` | ✓ | ✓ | UnifiedTask.TraceID |
| `group_id` | ✓ | ✓ | UnifiedTask.GroupID |
| `project_id` | ✓ | ✓ | itoa(ProjectID) |
| `user_id` | ✓ | ✓ | itoa(UserID) |
| `batch_id` | ✓ | — | `batch-<ULID>` |
| `is_hybrid` | ✓ | — | `"true"` \| `"false"` |
| `datapack` | — | ✓ | `payload.datapack.Name` |
| `dataset_id` | — | ✓ (BuildDatapack) | itoa |
| `execution_id` | — | ✓ (RunAlgorithm) | itoa |
| `timestamp` | — | ✓ (RunAlgorithm) | algo OUTPUT_PATH ts (added at runtime) |
| `job-name` | — | ✓ | `JobConfig.JobName` |

### CRD GVKs watched (via `chaosCli.GetCRDMapping()` from chaos-experiment):

```
chaos-mesh.org/v1alpha1: DNSChaos, HTTPChaos, JVMChaos, NetworkChaos, PodChaos, StressChaos, TimeChaos
```

Plus `Job` and `Pod` (`batch/v1`, `v1`) in `config.k8s.namespace` with label selector `rcabench_app_id=<AppID>` (`controller.go:85-100, 147`).

## 5. EventType catalog (used on `trace:<id>:log` stream)

From `consts/consts.go EventType`:

```
restart.pedestal.{started,completed,failed}
fault.injection.{started,completed,failed}
algorithm.run.{started,succeed,failed}
algorithm.{result.collection,no_result_data}
datapack.build.{started,succeed,failed}
datapack.{result.collection,no_anomaly,no_detector_data}
image.build.{started,succeed,failed}
task.{started,state.update,retry.status,scheduled}
no.{namespace,token}.available
acquire.lock | release.lock
k8s.job.{succeed,failed}
```

`consts.ValidTaskEvents map[TaskType][]EventType` enforces per-task-type whitelists.

## 6. DTO → Entity conversion map

Primary converter functions (not exhaustive; see `slices/05-data.md §5`):

| DTO | Converter | Target entity |
|---|---|---|
| `dto.CreateContainerReq` | `.ConvertToContainer()` | `database.Container` |
| `dto.CreateContainerVersionReq` | `.ConvertToContainerVersion()` | `database.ContainerVersion` |
| `dto.CreateDatasetReq` | `.ConvertToDataset()` | `database.Dataset` |
| `dto.CreateDatasetVersionReq` | `.ConvertToDatasetVersion()` | `database.DatasetVersion` |
| `dto.CreateProjectReq` | `.ConvertToProject()` | `database.Project` |
| `dto.CreateTeamReq` | `.ConvertToTeam()` | `database.Team` |
| `dto.CreateRoleReq` | `.ConvertToRole()` | `database.Role` |
| `dto.CreatePermissionReq` | `.ConvertToPermission()` | `database.Permission` |
| `dto.CreateLabelReq` | `.ConvertToLabel()` | `database.Label` |
| `dto.AssignUserPermissionItem` | `.ConvertToUserPermission()` | `database.UserPermission` |
| `dto.DetectorResultItem` | `.ConvertToDetectorResult(executionID)` | `database.DetectorResult` |
| `dto.GranularityResultItem` | `.ConvertToGranularityResult(executionID)` | `database.GranularityResult` |
| `dto.UnifiedTask` | `.ConvertToTask()` / `.ConvertToTrace(withAlgos, leafNum)` | `database.Task`, `database.Trace` |
| `dto.UploadDatapackReq` | `.ParseGroundtruths()` | `[]database.Groundtruth` |
| `dto.TraceStreamEvent` | `.ToRedisStream()` / `.ToSSE()` | Redis XStream msg + sse.Event |
| `dto.GroupStreamEvent` | `.ToRedisStream()` | Redis XStream msg |
| `dto.ConfigUpdateResponse` | `.ToMap()` | pub/sub JSON payload |
| `dto.TimeRangeQuery` | `.Convert()` | `*TimeFilterOptions` |
| `dto.List*Req` | `.ToFilterOptions()` | filter struct for GORM query |
| `dto.Search*Req` | `.ConvertToSearchReq[F]()` / `.ConvertToSearchRequest()` | `dto.SearchReq[F]` |

## 7. RBAC truth source — `consts/system.go`

- **Actions** (23): Create/Read/Update/Delete, Execute/Stop/Restart/Activate/Suspend, Upload/Download/Import/Export, Assign/Grant/Revoke, Configure/Manage, Share/Clone, Monitor/Analyze/Audit
- **Resources** (17): system, audit, configuration, container, container_version, dataset, dataset_version, project, team, label, user, role, permission, task, trace, injection, execution
- **Scopes**: `ProjectScopedResources = {injection, execution, task, trace}`; `TeamScopedResources = {container, container_version, dataset, dataset_version, project, label}`
- **Roles** (16): super_admin, admin, user, {container,dataset}_{admin,developer,viewer}, project_{admin,algo_developer,data_developer,viewer}, team_{admin,member,viewer}
- **Rule format**: `PermissionRule{Resource, Action, Scope}`, parseable from `"resource:action:scope"` strings
- **Matrix**: `SystemRolePermissions map[RoleName][]PermissionRule` — the only ACL spec; ~120 `PermXxx` constants compose it.

## 8. Entry points for configuration

`config/config.go Init` searches for `config.<ENV>.toml` in: `--conf` directory, `$HOME/.rcabench`, `/etc/rcabench`, `.`. Defaults: `ENV=dev`.

**Required fields** (`config.validate` fatals on missing):
```
name, version, port, workspace
database.mysql.{host,port,user,password,db}
redis.host
jaeger.endpoint
k8s.namespace
k8s.job.service_account.name
```

**Runtime-mutable state** (no viper.WatchConfig — all updates pushed via etcd or API):

- `detectorName` atomic (`config/config.go:17`) — consumed by dto/producer/consumer/init paths for detector-name dispatch
- `chaosConfigDB` package var (`config/chaos_system.go:21`)
- `chaosSystemConfigManager` singleton with `Reload(callback)` — loads from `systems` table then viper fallback
- DynamicConfig (DB) → etcd `/rcabench/configs/{producer,consumer,global}/<key>` → watchers → handlers
