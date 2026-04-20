# AegisLab Data-Shape Reference (v2)

> Archival note: this file was not fully revalidated after the phase-2 gRPC collapse and phase-6 module-wiring cleanup. Treat `docs/code-topology/README.md`, `docs/code-topology/slices/01-app-wiring.md`, and `docs/code-topology/slices/06-grpc-interfaces.md` as the current topology source of truth.

Quick-lookup tables. Cross-ref `slices/05-orchestration.md` and `slices/07-data-crosscut-seams.md`
for exhaustive field lists.

---

## 1. Entity inventory (`model/entity.go` — 39 entities)

All entities use custom soft-delete via `Status consts.StatusType` (-1=deleted, 0=disabled, 1=enabled)
+ MySQL **generated virtual columns** (`ActiveName`, `ActiveKeyValue`, etc.) for tombstone-respecting
unique indexes. **No `gorm.DeletedAt` anywhere**.

| Struct | Table | Key cols | FK | Notes |
|---|---|---|---|---|
| `DynamicConfig` | `dynamic_configs` | `(config_key, category)` uniq | `UpdatedBy→User` | M2M `Labels` via `config_labels` |
| `ConfigHistory` | `config_histories` | `ConfigID` idx | `Config`, `Operator→User`, self `RolledBackFrom` | |
| `Container` | `containers` | `ActiveName` uniq; status idx | — | M2M `Labels`; BeforeCreate validates pedestal name against `chaos.SystemType` |
| `ContainerVersion` | `container_versions` | semver `NameMajor/Minor/Patch`; `ActiveVersionKey` uniq | `Container`, `User` | 1:1 `HelmConfig`; M2M `EnvVars` via `container_version_env_vars` |
| `HelmConfig` | `helm_configs` | `ContainerVersionID` uniq | `ContainerVersion` CASCADE | M2M `DynamicValues` via `helm_config_values` |
| `ParameterConfig` | `parameter_configs` | `(key, type, category)` uniq | — | |
| `ContainerVersionEnvVar` | `container_version_env_vars` | composite PK | both FKs | M2M join |
| `HelmConfigValue` | `helm_config_values` | composite PK | both FKs | M2M join |
| `Dataset` | `datasets` | `ActiveName` uniq | — | M2M `Labels`, hasMany `Versions` |
| `DatasetVersion` | `dataset_versions` | semver; `ActiveVersionKey` uniq | `Dataset`, `User` | M2M `Datapacks` (= `FaultInjection`) via `dataset_version_injections` |
| `Project` | `projects` | `ActiveName` uniq; `TeamID` idx | `Team` | M2M `Containers`, `Datasets`, `Labels` |
| `Label` | `labels` | `ActiveKeyValue` uniq; `(key, category)` idx | — | |
| `Team` | `teams` | `ActiveName` uniq | — | hasMany `Projects` |
| `User` | `users` | `ActiveUsername` uniq; uniq `username`, `email` | — | BeforeCreate hashes password (SHA-256+salt — see orphans §A2) |
| `APIKey` ⭐ NEW | `api_keys` | `ActiveKeyID` uniq; `(owner, status)` idx | `User` | `Scopes []string` JSON; `KeySecretCiphertext` stores AES-GCM blob |
| `Role` | `roles` | `ActiveName` uniq | — | `IsSystem` flag |
| `Permission` | `permissions` | `(action, scope, resource_id)` idx; `ActiveName` uniq | `Resource` | |
| `Resource` | `resources` | uniq `Name`; `(category, parent_id)` | self `Parent` | |
| `AuditLog` | `audit_logs` | `(action, state)` idx; `(user_id, time)` idx | `User`, `Resource` | Audit writer **declared but not wired** — see orphans §D1 |
| `Trace` | `traces` | `(type, state)` idx; `(project_id, state)` idx; `GroupID` idx | `Project` | hasMany `Tasks`; PK string |
| `Task` | `tasks` | `(type, status)` idx; `TraceID`, `ParentTaskID` idx | `Trace` CASCADE; self `ParentTask` CASCADE | PK string(64) |
| `FaultInjection` | `fault_injections` | `(fault_type, state)` idx; `(benchmark_id, pedestal_id)` idx; `TaskID` idx; `start_time<end_time` check | `Benchmark→CV` SET NULL; `Pedestal→CV` SET NULL; `Task` CASCADE | `Groundtruths []Groundtruth` JSON (entity_helper.go scanner) |
| `Execution` | `executions` | `(algorithm_version_id, datapack_id)` idx | `Task` CASCADE; `AlgorithmVersion` RESTRICT; `Datapack→FaultInjection` RESTRICT; `DatasetVersion` SET NULL | hasMany `DetectorResult`, `GranularityResult` |
| `DetectorResult` | `detector_results` | per-`SpanName` rows with float stats | `Execution` CASCADE | |
| `GranularityResult` | `granularity_results` | `ExecutionID` idx; Level/Rank/Confidence | `Execution` CASCADE | |
| `System` | `systems` | uniq `Name` | — | `IsBuiltin` |
| `SystemMetadata` | `system_metadata` | `(system_name, metadata_type, service_name)` idx | — | `Data` JSON |
| `ContainerLabel`, `DatasetLabel`, `ProjectLabel`, `DatasetVersionInjection`, `FaultInjectionLabel`, `ExecutionInjectionLabel`, `ConfigLabel` | various join tables | composite PKs | | M2M joins |
| `UserContainer`, `UserDataset`, `UserProject`, `UserTeam` | `user_<resource>` | `Active*` uniq | `User`, `<Resource>`, `Role` | `UserProject` carries `WorkspaceConfig` JSON |
| `UserRole` | `user_roles` | `(user_id, role_id)` uniq | `User`, `Role` | global roles |
| `RolePermission` | `role_permissions` | `(role_id, permission_id)` uniq | `Role`, `Permission` | |
| `UserPermission` | `user_permissions` | 3 composite uniques (per scope: container/dataset/project) | `User`, `Permission`, optional scope | `GrantType` (grant/deny); `ExpiresAt` |
| `Evaluation` ⭐ NEW | `evaluations` | `ProjectID` idx; `Status` idx | — (string-only links to algo/dataset by name+version) | `EvalType`, precision/recall/f1 |

**Views** (`model/view.go`; recreated on every boot by `infra/db/migration.go`):

- `fault_injection_no_issues` → `FaultInjectionNoIssues`
- `fault_injection_with_issues` → `FaultInjectionWithIssues`
  Both LEFT-JOIN fault-injection labels and use a window-function-ranked subquery keyed on
  hard-coded `algorithm_id = 1` (orphans §C1).

**SDK-owned tables** (outside AutoMigrate; Python SDK writes, `module/sdk` reads):

- `data` → `SDKDatasetSample` (`module/sdk/models.go`)
- `evaluation_data` → `SDKEvaluationSample`

## 2. FK / M2M graph (condensed)

```
User ─┬── UserRole ─── Role ─── RolePermission ─── Permission ─── Resource ─(self.Parent)
      ├── UserTeam ── Team ── Project (TeamID)
      ├── UserProject ── Project
      ├── UserContainer ── Container
      ├── UserDataset ── Dataset
      ├── UserPermission ── Permission (optional container/dataset/project)
      ├── APIKey  (Scopes JSON, KeySecretCiphertext AES-GCM)
      ├── AuditLog ── Resource
      └── ConfigHistory.Operator, DynamicConfig.UpdatedBy

Project ─── Trace ─── Task (self.ParentTask CASCADE)
             │              │
             │              ├── FaultInjection (1:1 CASCADE)
             │              └── Execution      (1:1 CASCADE)
             │
  Container ─── ContainerVersion ─(1-1)─ HelmConfig
                               ├── M2M (env_vars) ─── ParameterConfig
                               └── M2M (helm_values) ── ParameterConfig  (via HelmConfig)
  Dataset ─── DatasetVersion ─── (M2M dataset_version_injections) ─── FaultInjection
  Label ─── all six M2M join tables (container/dataset/project/fault_injection/execution_injection/config)

  FaultInjection ─ Benchmark→ContainerVersion  (SET NULL)
                 ─ Pedestal  →ContainerVersion (SET NULL)
  Execution ─ AlgorithmVersion →ContainerVersion   (RESTRICT)
            ─ Datapack         →FaultInjection     (RESTRICT)
            ─ DatasetVersion   →DatasetVersion     (SET NULL)
  Execution ─< DetectorResult CASCADE
  Execution ─< GranularityResult CASCADE

  System — standalone (link to SystemMetadata by string SystemName only)
  Evaluation — standalone (links to algorithm/dataset by name+version strings)
```

## 3. Redis key catalog (complete)

| Key / stream | Redis type | Written by | Read by |
|---|---|---|---|
| `task:delayed` | ZSET (score = epoch sec exec time) | `SubmitDelayedTask` (`infra/redis/task_queue.go:69`), cron reschedule | Scheduler `ProcessDelayedTasks` (Lua); `Expedite`; `CancelTask` |
| `task:ready` | LIST (FIFO) | scheduler promote; `SubmitImmediateTask` | `GetTask` BRPOP; `CancelTask` |
| `task:dead` | ZSET | `HandleFailedTask` | `ListDeadLetterTasks` RPC; `CancelTask` |
| `task:index` | HASH `taskID → queueKey` | every submit / promote / expedite | `GetTaskQueue`; `DeleteTaskIndex` |
| `task:concurrency_lock` | STRING counter | `AcquireConcurrencyLock` INCR | `ReleaseConcurrencyLock` DECR; `InitConcurrencyLock` at worker startup |
| `trace:<id>:log` (`consts.StreamTraceLogKey`) | XStream | `EmitTaskScheduled`; every `publishEvent` call in consumer/k8s_handler (~30 sites); `module/task/service.go:137` Expedite | SSE handler `module/trace/handler.go`; `trace.Service.ReadTraceStreamMessages`; `orchestratorclient.ReadTraceStreamMessages` RPC |
| `group:<id>:log` (`consts.StreamGroupLogKey`) | XStream | `service/consumer/trace.go` (terminal transitions only) | SSE handler `module/group/handler.go`; `group.Service.ReadGroupStreamMessages` |
| `notifications:global` (`consts.NotificationStreamKey`) | XStream | `module/system`, `ratelimiter.GC` (outside orchestration slice) | SSE handler `module/notification/handler.go` (**no frontend caller — orphans §E6**) |
| `monitor:namespaces` (`consts.NamespacesKey`) | SET | `consumer/monitor` via `namespaceCatalogStore.seed` | `namespaceCatalogStore.list`, `InitializeNamespaces`, `RefreshNamespaces` |
| `monitor:ns:<ns>` (`consts.NamespaceKeyPattern`) | HASH `{end_time, trace_id, status}` | `namespaceLockStore.acquire/release/write`; `namespaceStatusStore.set` | `AcquireLock`, `isNamespaceLocked`, `getNamespaceStatus` |
| `token_bucket:restart_service` | SET of taskIDs | Lua SADD in `rate_limiter_store.go:31` | Lua SCARD, `ReleaseToken`, `ratelimiter.GC` |
| `token_bucket:build_container` | SET of taskIDs | same | same |
| `token_bucket:algo_execution` | SET of taskIDs | same | same |
| `injection:algorithms` (`consts.InjectionAlgorithmsKey`) | HASH `groupID → JSON []ContainerVersionItem` | `module/injection/service.go:298` (on submit with algorithms) | `service/consumer/redis.go:41` (`loadCachedInjectionAlgorithms`); `module/trace/service.go:68-70` |
| `joblogs:<taskID>` | Pub/Sub channel (prefix `PubSubChannelPrefix`) | `service/logreceiver/receiver.go:246` `publisher.Publish` | `module/task/queue_store.go:22` `SubscribeJobLogs` → WS handler |
| `config:updates:response` (`consts.ConfigUpdateResponseChannel`) | Pub/Sub channel | `service/common/config_registry.go:84` `PublishWrapper` | producer-side waiters (system, ratelimiter, others) |
| `blacklist:token:<jti>` | STRING w/ TTL | `module/auth/token_store.go` `AddTokenToBlacklist` | `IsTokenBlacklisted` (SCAN is the cost) |
| `api_key:nonce:<keyID>:<nonce>` | STRING w/ TTL (SETNX) | `TokenStore.ReserveAPIKeyNonce` during `ExchangeAPIKeyToken` | same key name for replay check |
| `last_batch_info` | string const only | **declared but never read/written** — dead | — |

## 4. Task types (`consts.TaskType`)

| # | Enum | Executor | Submitted by | Next task on success |
|---|---|---|---|---|
| 0 | `BuildContainer` | `executeBuildContainer` | `POST /containers/build` handler → `SubmitTaskWithDB` | — (leaf) |
| 1 | `RestartPedestal` | `executeRestartPedestal` | `module/injection/service.SubmitFaultInjection` | `common.ProduceFaultInjectionTasksWithDB` → `FaultInjection` |
| 2 | `FaultInjection` | `executeFaultInjection` | `common.ProduceFaultInjectionTasksWithDB` | — (CRD callback `HandleCRDSucceeded` chains) |
| 3 | `RunAlgorithm` | `executeAlgorithm` | `HandleJobSucceeded` (BuildDatapack branch) for detector; `collect_result` fan-out for user algos; `POST /executions/execute` handler | — (Job callback `HandleJobSucceeded` chains) |
| 4 | `BuildDatapack` | `executeBuildDatapackWithDeps` | `HandleCRDSucceeded`; `POST /injections/build` handler | detector `RunAlgorithm` |
| 5 | `CollectResult` | `executeCollectResult` | `HandleJobSucceeded` (RunAlgorithm branch) | optional fan-out of more `RunAlgorithm` |
| 6 | `CronJob` | ❌ **NO dispatch case** | scheduler cron path only | self-rescheduled by scheduler |

## 5. K8s label / annotation schema

Set on Chaos Mesh CRDs and Jobs (`consts/consts.go:452-479`; applied by
`service/consumer/fault_injection.go:208-228`, `build_datapack.go:79-86`, `algo_execution.go:131-138`,
`dto.UnifiedTask.GetLabels/GetAnnotations`).

### Annotations

| Key | On CRD | On Job | Value |
|---|---|---|---|
| `task_carrier` (`TaskCarrier`) | ✓ | ✓ | OTel propagator MapCarrier JSON (task span) |
| `trace_carrier` (`TraceCarrier`) | ✓ | ✓ | OTel propagator MapCarrier JSON (trace span) |
| `benchmark` (`CRDAnnotationBenchmark`) | ✓ | — | JSON `dto.ContainerVersionItem` |
| `algorithm` (`JobAnnotationAlgorithm`) | — | ✓ (RunAlgorithm) | JSON `dto.ContainerVersionItem` |
| `datapack` (`JobAnnotationDatapack`) | — | ✓ (BuildDatapack + RunAlgorithm) | JSON `dto.InjectionItem` |

### Labels

| Key | On CRD | On Job | Value |
|---|---|---|---|
| `rcabench_app_id` (`K8sLabelAppID`) | ✓ | ✓ | consumer-startup ULID (`consts.AppID`); informer LabelSelector |
| `task_id` (`JobLabelTaskID`) | ✓ | ✓ | `UnifiedTask.TaskID` |
| `task_type` (`JobLabelTaskType`) | ✓ | ✓ | `consts.GetTaskTypeName` |
| `trace_id` | ✓ | ✓ | `UnifiedTask.TraceID` |
| `group_id` | ✓ | ✓ | `UnifiedTask.GroupID` |
| `project_id` | ✓ | ✓ | itoa |
| `user_id` | ✓ | ✓ | itoa |
| `batch_id` (`CRDLabelBatchID`) | ✓ | — | `batch-<ULID>` |
| `is_hybrid` (`CRDLabelIsHybrid`) | ✓ | — | `"true"` \| `"false"` |
| `datapack` (`JobLabelDatapack`) | — | ✓ | `payload.datapack.Name` |
| `dataset_id` (`JobLabelDatasetID`) | — | ✓ (BuildDatapack) | itoa |
| `execution_id` (`JobLabelExecutionID`) | — | ✓ (RunAlgorithm) | itoa |
| `timestamp` (`JobLabelTimestamp`) | — | ✓ (RunAlgorithm) | set at runtime by `algo_execution.go` |
| `job-name` (`JobLabelName`) | — | ✓ | `JobConfig.JobName` |

CRD GVKs watched (via `chaosCli.GetCRDMapping()` from chaos-experiment):
```
chaos-mesh.org/v1alpha1: DNSChaos, HTTPChaos, JVMChaos, NetworkChaos, PodChaos, StressChaos, TimeChaos
```

Plus `batch/v1 Jobs` + `core/v1 Pods` (filtered by `rcabench_app_id=<AppID>` + `config.k8s.namespace`).

## 6. EventType catalog (on `trace:<id>:log`)

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

`consts.ValidTaskEvents map[TaskType][]EventType` enforces per-task-type whitelists. `consts.SSEEventName`
provides `consts.EventEnd` as the terminator for SSE replay streams.

## 7. gRPC surface (total: 132 RPCs across 5 services)

| Service | Port | RPCs | Local HandlerServices called |
|---|---|---|---|
| iam (port 9091) | 61 | `VerifyToken`, `CheckPermission`, `Login/Register/RefreshToken/Logout`, API-keys (8 RPCs), user mgmt (5 + 10 assignment RPCs), role mgmt (5 + 2 role-perm + 1 users-of-role), permissions (3), resources (3), teams (10), membership checks (5) | `auth`, `rbac`, `user`, `team`, `middleware.Service` |
| orchestrator (9092) | 28 | Submit (3), runtime mutations (7), exec/inj reads (4), tasks (3), traces/groups (4), metrics (2), evaluations (2), project statistics (1), dead-letter (2), streams (4 polling RPCs) | `execution`, `injection`, `task`, `trace`, `group`, `metric`, `notification`, `project.Repository`, plus an in-file `taskQueueController` |
| resource (9093) | 25 | Projects RO (2), Containers RO (2), Datasets RO (2), Labels (6), ChaosSystems (7), Evaluations (6) | `project`, `container`, `dataset`, `label`, `chaossystem`, `evaluation` |
| runtime (9094) | 6 | `Ping`, `GetRuntimeStatus`, `GetQueueStatus`, `GetLimiterStatus`, `GetNamespaceLocks`, `GetQueuedTasks` | `consumer.RuntimeSnapshotService` + direct Redis reads |
| system (9095) | 12 | `Ping`, Health/Metrics/SystemInfo, Configs (2), AuditLogs (2), NamespaceLocks/QueuedTasks, SystemMetrics (2) | `system`, `systemmetric` |

All servers: plain TCP (no TLS), `grpc_health_v1`, `httpx.UnaryServerRequestIDInterceptor`,
`structpb.Struct` payload encoding, `mapErrorToStatus` for `consts.Err*` → gRPC code.

Client-side config keys: `clients.<name>.target` preferred; legacy `<name>.grpc.target` fallback.
`runtime-worker` uses `clients.runtime.target` / `runtime_worker.grpc.target` (naming drift).

## 8. RBAC truth source (`consts/system.go`)

- **Actions** (23): Create, Read, Update, Delete, Execute, Stop, Restart, Activate, Suspend,
  Upload, Download, Import, Export, Assign, Grant, Revoke, Configure, Manage, Share, Clone,
  Monitor, Analyze, Audit
- **Resources** (17): system, audit, configuration, container, container_version, dataset,
  dataset_version, project, team, label, user, role, permission, task, trace, injection, execution
- **Scopes**: `ProjectScopedResources = {injection, execution, task, trace}`;
  `TeamScopedResources = {container, container_version, dataset, dataset_version, project, label}`;
  helpers `IsProjectScoped`, `IsTeamScoped`
- **Roles** (16): super_admin, admin, user, {container,dataset}\_{admin,developer,viewer},
  project_{admin, algo_developer, data_developer, viewer}, team_{admin, member, viewer}
- **Rule format**: `PermissionRule{Resource, Action, Scope}`; parseable from
  `"resource:action:scope"` (`ParsePermissionRule`)
- **Matrix**: `SystemRolePermissions map[RoleName][]PermissionRule` at `consts/system.go:349` —
  ~80 `PermXxx` predefined rule constants compose it. Seeded at startup by
  `service/initialization/producer.go`.

API-key scopes are a **separate** enforcement layer (`middleware/api_key_scope.go`):

- String scopes like `"sdk:evaluations:read"`, `"sdk:*"`, `"*"`
- `apiKeyScopeMatchesTarget(scope, target)` — dot-colon glob matcher
- `RequireAPIKeyScopesAny("sdk:*", "sdk:evaluations:read")` — checked **only** when
  `auth_type == "api_key"`; user JWTs bypass. So SDK scope gates are effectively
  **machine-only** authorization, with permission checks still running afterward.

## 9. DTO → entity conversion map (selected)

Every module's `api_types.go` has its own converters. These are cross-slice converters worth
knowing:

| DTO | Converter | Target entity |
|---|---|---|
| `module/auth.CreateAPIKeyReq` | `.ConvertToAPIKey()` | `model.APIKey` |
| `module/container.CreateContainerReq` | `.ConvertToContainer()` | `model.Container` |
| `module/injection.SubmitInjectionReq` | `.ResolveSpecs(FriendlySpecToNode)` | `[]chaos.Node` or `[]guidedcli.GuidedConfig` (branch) |
| `module/injection.UploadDatapackReq` | `.ParseGroundtruths()` | `[]model.Groundtruth` |
| `dto.UnifiedTask` (shared) | `.ConvertToTask()` / `.ConvertToTrace(withAlgos, leafNum)` | `model.Task`, `model.Trace` |
| `dto.TraceStreamEvent` | `.ToRedisStream()` / `.ToSSE()` | Redis XStream msg + sse.Event |
| `dto.ConfigUpdateResponse` | `.ToMap()` | pub/sub JSON payload |

## 10. Configuration

`config/config.Init` (still imported via `infra/config.Module` shim) searches for
`config.<ENV>.toml` in: `--conf` directory, `$HOME/.rcabench`, `/etc/rcabench`, `.`.

**Required fields** (fatals if missing, `config.validate`):
```
name, version, port, workspace
database.mysql.{host, port, user, password, db}
redis.host
jaeger.endpoint               (actually OTLP-HTTP endpoint — file name drift)
k8s.namespace
k8s.job.service_account.name
```

**Microservice target keys** (checked by `RequireConfiguredTargets`):
```
clients.iam.target           (legacy: iam.grpc.target)
clients.orchestrator.target  (legacy: orchestrator.grpc.target)
clients.resource.target      (legacy: resource.grpc.target)
clients.system.target        (legacy: system.grpc.target)
clients.runtime.target       (legacy: runtime_worker.grpc.target)   ← note underscore
```

**Runtime-mutable state** (no viper.WatchConfig; updates go via etcd or API):

- `detectorName atomic.Value` (`config/config.go:17`) — consumed in task-dispatch hot paths
- `chaosConfigDB` package var (`config/chaos_system.go:21`)
- `chaosSystemConfigManager` singleton — `Reload(callback)` re-reads from `systems` table
  (falls back to viper-backed `injection.system` map)
- DynamicConfig rows → etcd `/rcabench/configs/{producer,consumer,global}/<key>` → watchers → handlers

## 11. Frontend → backend endpoint usage (from `AegisLab-frontend/src/api/`)

`BASE_PATH = '/api/v2'`. Flat URLs — **the router's audience split (admin/portal/public/sdk)
is URL-transparent**. Hand-rolled `apiClient.<method>('/path')` mixed with `@rcabench/client` SDK
via `sdkAxios`.

Deduped endpoint list (alphabetical, deduped): see `slices/07-data-crosscut-seams.md §10`.

Notable:

- SSE literals: `/api/v2/traces/{id}/stream`, `/api/v2/groups/{id}/stream`
- WS literal: `/api/v2/tasks/{id}/logs/ws?token=...`
- **No frontend caller for `/api/v2/notifications/stream`** despite being wired
- **No frontend caller for `/api/v2/sdk/*` endpoints** — SDK surface is machine-only

## 12. rcabench-platform data contract

AegisLab writes into `jfs.dataset_path`:

```
<datapack>/
├── trace.parquet            — distributed traces
├── log.parquet              — app logs
├── metrics.parquet          — time-series metrics
├── metrics_sli.parquet      — SLI metrics with anomaly detection
└── injection.json           — ground-truth fault injection info
```

Read by rcabench-platform CLIs:

- `cli/dataset_analysis/detector_cli.py:98, 208`
- `cli/dataset_analysis/scan_datasets.py:40-50`
- `notebooks/sdg.py:135`

Platform-side output files (NOT written by AegisLab): `index.parquet`, `labels.parquet`,
`conclusion.parquet`, `perf.parquet`, `ranks.parquet`.

## 13. chaos-experiment import surface (unchanged since v1)

AegisLab imports **only** from 3 top-level subpackages of chaos-experiment:

| Subpackage | Alias | Used by |
|---|---|---|
| `chaos-experiment/handler` | `chaos` | consts, model, utils/fault_translate, config/chaos_system, service/{consumer,common,initialization}, module/{injection,execution,evaluation,dataset,chaossystem} |
| `chaos-experiment/client` | `chaosCli` | `infra/chaos/module.go`, `infra/k8s/controller.go` |
| `chaos-experiment/pkg/guidedcli` | — | `cmd/aegisctl/cmd/inject_guided.go`, `service/consumer/fault_injection.go`, `module/injection/{api_types, submit}.go` |

Not referenced: `chaos`, `cmd`, `controllers`, `docs`, `internal`, `tools`, `utils`
(stale docs may still mention them).

## 14. Go module dependencies worth knowing (new since refactor)

Direct requires in `go.mod` introduced by the refactor:

- `go.uber.org/fx v1.24.0` — the entire DI kernel
- `google.golang.org/grpc v1.75.0`, `google.golang.org/protobuf v1.36.8` — gRPC transport
- `github.com/ClickHouse/clickhouse-go/v2 v2.34.0` — telemetry DB (via `infra/db.NewDatabaseConfig("clickhouse")`)

Unchanged direct deps (carried over):

- Gin, GORM, viper, cobra, go-redis/v9, etcd/v3, controller-runtime, helm/v3, buildkit,
  duckdb-go/v2, arrow-go/v18, OpenTelemetry, go-playground/validator, gorilla/websocket,
  swaggo, golang-jwt/v5, oklog/ulid, distribution/reference, sigs.k8s.io/yaml +
  stretchr/testify/assert/yaml (orphans §E5)
