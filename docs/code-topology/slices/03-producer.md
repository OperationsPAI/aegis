# 03 — Synchronous Service Layer (producer / common / analyzer)

Source roots:
- `src/service/producer/*.go`
- `src/service/common/*.go`
- `src/service/analyzer/evaluation.go`

All citations `<file>:<line>` are relative to `src/service/`.

## 1. File → domain map

### producer/

| file | purpose | exported funcs |
|---|---|---|
| audit.go | audit log persistence + listing | `GetAuditLogDetail`, `ListAuditLogs`, `LogFailedAction`, `LogSystemAction`, `LogUserAction` (audit.go:15,28,50,85,118) |
| auth.go | user register/login/logout/refresh, profile, JWT issuance | `Register`, `Login`, `Logout`, `RefreshToken`, `ChangePassword`, `GetProfile` (auth.go:20,66,123,136,168,203) |
| chaos_system.go | CRUD on `database.System` + register/unregister via `chaos.RegisterSystem` | `ListChaosSystemsService`, `GetChaosSystemService`, `CreateChaosSystemService`, `UpdateChaosSystemService`, `DeleteChaosSystemService`, `UpsertChaosSystemMetadataService`, `ListChaosSystemMetadataService` (chaos_system.go:17,37,46,83,140,163,185) |
| common.go | shared helper for resolving datapacks from name or dataset ref | (no exports; `extractDatapacks`, `checkLabelKeyValue` private) |
| container.go | Container + ContainerVersion CRUD; helm chart/values upload; container build task production | `CreateContainer`, `CreateContainerCore`, `DeleteContainer`, `GetContainerDetail`, `ListContainers`, `UpdateContainer`, `ManageContainerLabels`, `CreateContainerVersion`, `DeleteContainerVersion`, `GetContainerVersionDetail`, `ListContainerVersions`, `UpdateContainerVersion`, `SetContainerVersionImage`, `UploadHelmChart`, `UploadHelmValueFile`, `UploadHemlValueFileCore`, `ProduceContainerBuildingTask` (container.go:30,56,97,125,148,182,212,288,315,328,363,384,428,465,523,542,594) |
| dataset.go | Dataset + DatasetVersion CRUD + zip download | `CreateDataset`, `CreateDatasetCore`, `DeleteDataset`, `GetDatasetDetail`, `ListDatasets`, `SearchDatasets`, `UpdateDataset`, `ManageDatasetLabels`, `CreateDatasetVersion`, `DeleteDatasetVersion`, `GetDatasetVersionDetail`, `ListDatasetVersions`, `UpdateDatasetVersion`, `GetDatasetVersionFilename`, `DownloadDatasetVersion`, `ManageDatasetVersionInjections` (dataset.go:25,61,102,124,148,182,205,232,303,337,349,370,391,420,441,460) |
| dynamic_config.go | Producer-side wiring for `database.DynamicConfig` (CRUD, etcd push, history, rollback) | `GetConfigDetail`, `ListConfigs`, `RollbackConfigValue`, `RollbackConfigMetadata`, `UpdateConfigValue`, `UpdateConfigMetadata`, `ListConfigHistories` (dynamic_config.go:65,85,106,161,214,257,309) |
| evaluation.go | thin Evaluation read/delete | `ListEvaluations`, `GetEvaluation`, `DeleteEvaluation` (evaluation.go:11,32,42) |
| execution.go | Execution CRUD, label mgmt, detector/granularity result upload, algo execution task production | `BatchCreateDetectorResults`, `BatchCreateGranularityResults`, `BatchDeleteExecutionsByIDs`, `BatchDeleteExecutionsByLabels`, `GetExecutionDetail`, `ListExecutions`, `ListAvaliableExecutionLabels`, `ManageExecutionLabels`, `ProduceAlgorithmExeuctionTasks` (execution.go:21,51,80,91,115,161,202,225,291) |
| group.go | Group-level trace stats + Redis stream SSE driver | `GetGroupStats`, `GroupStreamProcessor`, `NewGroupStreamProcessor`, `ProcessGroupMessage`, `IsCompleted`, `ReadGroupStreamMessages` (group.go:19,68,74,91,123,128) |
| injection.go | FaultInjection CRUD/clone/labels/search; datapack file tree + zip; **fault-injection task production** + `ProduceDatapackBuildingTasks` | `BatchDeleteInjectionsByIDs`, `BatchDeleteInjectionsByLabels`, `CreateInjection`, `UpdateGroundtruth`, `GetInjectionDetail`, `CloneInjection`, `GetInjectionLogs`, `ListInjections`, `SearchInjections`, `GetDatapackFilename`, `DownloadDatapack`, `GetDatapackFiles`, `DownloadDatapackFile`, `ListInjectionsNoIssues`, `ListInjectionsWithIssues`, `ManageInjectionLabels`, `BatchManageInjectionLabels`, `ProduceRestartPedestalTasks`, `ProduceDatapackBuildingTasks` (injection.go:44,55,79,111,121,148,188,238,273,339,356,377,417,463,501,539,608,806,1023) |
| label.go | Label CRUD + cross-entity association cleanup | `BatchDeleteLabels`, `CreateLabel`, `CreateLabelCore`, `DeleteLabel`, `GetLabelDetail`, `ListLabels`, `UpdateLabel` (label.go:15,100,125,155,210,223,245) |
| metrics.go | aggregate counts/duration over `FaultInjection`, `Execution`, algorithms via raw GORM | `GetInjectionMetrics`, `GetExecutionMetrics`, `GetAlgorithmMetrics` (metrics.go:12,98,178) |
| notification.go | passthrough to `client.RedisXRead` for notification stream | `ReadNotificationStreamMessages` (notification.go:13) |
| permission.go | permission CRUD + `CheckUserPermission` | `CheckUserPermission`, `GetPermissionDetail`, `ListPermissions`, `ListRolesFromPermission` (permission.go:15,32,45,65) |
| project.go | Project CRUD + project-injection / project-execution listing + middleware perm helpers | `CreateProject`, `DeleteProject`, `GetProjectDetail`, `ListProjects`, `UpdateProject`, `ManageProjectLabels`, `ListProjectInjections`, `ListProjectExecutions`, `IsUserInProject`, `IsUserProjectAdmin`, `IsProjectPublic`, `GetProjectTeamID` (project.go:17,61,80,110,165,197,285,320,357,369,381,393) |
| query_datapack_arrow.go | DuckDB → Arrow IPC stream of parquet files (`duckdb_arrow` build tag) | `QueryDatapackFileContent` (query_datapack_arrow.go:19) |
| query_datapack_noarrow.go | stub returning error when built without `duckdb_arrow` | `QueryDatapackFileContent` (query_datapack_noarrow.go:13) |
| rate_limiter.go | Redis token-bucket inspect/reset/GC (set semantics) | `ListRateLimiters`, `ResetRateLimiter`, `GCRateLimiters` (rate_limiter.go:36,88,106) |
| relation.go | user-role/permission/container/dataset/project association mgmt | `AssignRoleToUser`, `RemoveRoleFromUser`, `BatchAssignUserPermissions`, `BatchRemoveUserPermissions`, `BatchAssignRolePermissions`, `RemovePermissionsFromRole`, `ListUsersFromRole`, `AssignContainerToUser`, `RemoveContainerFromUser`, `AssignDatasetToUser`, `RemoveDatasetFromUser`, `AssignProjectToUser`, `RemoveProjectFromUser` (relation.go:17,51,82,173,204,247,270,295,337,370,412,445,487) |
| resource.go | resource & resource-perm read | `GetResourceDetail`, `ListResources`, `ListResourcePermissions` (resource.go:15,28,49) |
| role.go | Role CRUD | `CreateRole`, `DeleteRole`, `GetRoleDetail`, `ListRoles`, `UpdateRole` (role.go:15,38,82,113,134) |
| sdk_evaluation.go | SDK eval/dataset sample read | `ListSDKEvaluations`, `GetSDKEvaluation`, `ListSDKExperiments`, `ListSDKDatasetSamples` (sdk_evaluation.go:12,27,36,45) |
| spec_convert.go | convert `dto.FriendlyFaultSpec` → `chaos.Node` tree (legacy injection path) | `FriendlySpecToNode` (spec_convert.go:31) |
| system.go | namespace lock inspection, queued-task listing, host CPU/mem/disk metrics | `InspectLock`, `ListQueuedTasks`, `GetSystemMetrics`, `GetSystemMetricsHistory`, `StoreSystemMetrics` (system.go:24,71,116,163,234) — also `init()` starts a 1-min metric goroutine (system.go:269) |
| task.go | Task expedite/list/detail; **WebSocket log streamer** subscribing Redis pub/sub + Loki | `ExpediteTask`, `BatchDeleteTasks`, `GetTaskDetail`, `ListTasks`, `TaskLogStreamer`, `NewTaskLogStreamer`, `(*TaskLogStreamer).StreamLogs/WriteMessage/ForwardRedisLog` (task.go:67,128,140,179,37,45,206,268,280) |
| team.go | Team + UserTeam mgmt + team perm helpers | `CreateTeam`, `DeleteTeam`, `GetTeamDetail`, `ListTeams`, `UpdateTeam`, `ListTeamProjects`, `AddTeamMember`, `RemoveTeamMember`, `UpdateTeamMemberRole`, `ListTeamMembers`, `IsUserInTeam`, `IsUserTeamAdmin`, `IsTeamPublic` (team.go:16,55,67,96,127,146,178,224,252,299,348,360,372) |
| trace.go | Trace read + per-trace Redis stream SSE driver, payload type registry | `GetTraceDetail`, `ListTraces`, `StreamProcessor`, `NewStreamProcessor`, `(*StreamProcessor).IsCompleted/ProcessMessageForSSE`, `GetTraceStreamProcessor`, `ReadTraceStreamMessages` (trace.go:42,56,83,89,102,106,147,167) |
| upload.go | manual datapack zip upload → unzip into `jfs.dataset_path` + create FaultInjection row | `UploadDatapack` (upload.go:33) |
| user.go | User CRUD | `CreateUser`, `DeleteUser`, `GetUserDetail`, `ListUsers`, `UpdateUser`, `SearchUsers`, `IsUserSystemAdmin` (user.go:16,62,104,149,174,206,211) |

### common/

| file | purpose | exported funcs/types |
|---|---|---|
| config_listener.go | singleton etcd watcher; per-scope `EnsureScope` loads MySQL defaults into etcd & viper, then watches | `GetConfigUpdateListener`, `(*configUpdateListener).EnsureScope/Stop` (config_listener.go:44,66,97) |
| config_registry.go | scope+category registry of `ConfigHandler`; routes etcd events; bundles `algoConfigHandler` | `ConfigHandler` (iface), `RegisterHandler`, `RegisterGlobalHandlers`, `ListRegisteredConfigKeys`, `PublishWrapper` (config_registry.go:20,44,52,61,80) |
| container.go | parameter-spec rendering (fixed + dynamic templates), container-ref → version mapping, helm value listing | `ListContainerVersionEnvVars`, `ListHelmConfigValues`, `MapRefsToContainerVersions` (container.go:13,18,23) |
| dataset.go | dataset-ref → DatasetVersion mapping (uses `repository.BatchGetDatasetVersions`) | `MapRefsToDatasetVersions` (dataset.go:11) |
| dynamic_config.go | DynamicConfig DB create + value/metadata validation | `CreateConfig`, `ValidateConfig`, `ValidateConfigMetadataConstraints` (dynamic_config.go:54,65,131) |
| injection.go | thin wrapper enqueueing a `TaskTypeFaultInjection` via `SubmitTask` | `ProduceFaultInjectionTasks` (injection.go:13) |
| label.go | label create-or-update across categories; usage counter mgmt | `ConvertLabelFiltersToConditions`, `CreateOrUpdateLabelsFromItems`, `GetLabelConditionsByItems` (label.go:16,34,101) |
| metadata_store.go | implements `chaos.MetadataStore` reading `system_metadata` rows with `sync.Map` cache | `DBMetadataStore`, `NewDBMetadataStore`, `(*).GetServiceEndpoints/GetAllServiceNames/GetJavaClassMethods/GetDatabaseOperations/GetGRPCOperations/GetNetworkPairs/GetRuntimeMutatorTargets/InvalidateCache` (metadata_store.go:15,20,28,51,66,89,112,135,163,168) |
| task.go | **the canonical task enqueue path** | `CronNextTime`, `SubmitTask`, `EmitTaskScheduled` (task.go:22,51,145) |
| template.go | `{{ .Field }}` → struct-field substitution for parameter rendering | (no exports; `extractTemplateVars`, `renderTemplate` private) |

### analyzer/

| file | purpose | exported funcs |
|---|---|---|
| evaluation.go | batch evaluate algorithms × datapacks/datasets, persist `database.Evaluation` rows with `ResultJSON` | `ListDatapackEvaluationResults`, `ListDatasetEvaluationResults` (evaluation.go:17,113) |

---

## 2. Producer → repository edge list

| file | repository.* calls (deduped) |
|---|---|
| audit.go | `GetAuditLogByID`, `ListAuditLogs`, `GetResourceByName`, `CreateAuditLog` |
| auth.go | `GetUserByUsername`, `GetUserByEmail`, `CreateUser`, `UpdateUserLoginTime`, `ListRolesByUserID`, `AddTokenToBlacklist`, `GetUserByID`, `UpdateUser`, `ListUserContainersByUserID`, `ListUserDatasetsByUserID`, `ListUserProjectsByUserID` |
| chaos_system.go | `ListSystems`, `GetSystemByID`, `CreateSystem`, `UpdateSystem`, `DeleteSystem`, `UpsertSystemMetadata`, `ListSystemMetadata` |
| common.go | `GetInjectionByName`, `ListInjectionsByDatasetVersionID` |
| container.go | `GetRoleByName`, `CreateContainer`, `CreateUserContainer`, `BatchDeleteContainerVersions`, `RemoveUsersFromContainer`, `ClearContainerLabels`, `DeleteContainer`, `GetContainerByID`, `ListContainerVersionsByContainerID`, `ListContainers`, `ListContainerLabels`, `UpdateContainer`, `AddContainerLabels`, `ListLabelIDsByKeyAndContainerID`, `BatchDecreaseLabelUsages`, `ListLabelsByContainerID`, `BatchCreateContainerVersions`, `BatchCreateOrFindParameterConfigs`, `ListParameterConfigsByKeys`, `AddContainerVersionEnvVars`, `BatchCreateHelmConfigs`, `AddHelmConfigValues`, `ListContainersByID`, `GetContainerVersionByID`, `ListContainerVersions`, `UpdateContainerVersion`, `UpdateContainerVersionImageColumns`, `GetHelmConfigByContainerVersionID`, `UpdateHelmConfig` |
| dataset.go | `GetRoleByName`, `CreateDataset`, `CreateUserDataset`, `BatchDeleteDatasetVersions`, `RemoveUsersFromDataset`, `DeleteDataset`, `GetDatasetByID`, `ListDatasetVersionsByDatasetID`, `ListDatasets`, `ListDatasetLabels`, `ExecuteSearch`, `UpdateDataset`, `AddDatasetLabels`, `ListLabelIDsByKeyAndDatasetID`, `ClearDatasetLabels`, `BatchDecreaseLabelUsages`, `ListLabelsByDatasetID`, `BatchCreateDatasetVersions`, `ListDatasetsByID`, `ListInjectionIDsByNames`, `AddDatasetVersionInjections`, `ClearDatasetVersionInjections`, `UpdateDatasetVersion`, `DeleteDatasetVersion`, `GetDatasetVersionByID`, `ListDatasetVersions`, `ListInjectionsByDatasetVersionID` |
| dynamic_config.go | `GetConfigByID`, `ListConfigHistoriesByConfigID`, `ListConfigs`, `GetConfigHistory`, `UpdateConfig`, `CreateConfigHistory`, `ListConfigHistories` |
| evaluation.go | `ListEvaluations`, `GetEvaluationByID`, `DeleteEvaluation` |
| execution.go | `SaveDetectorResults`, `SaveGranularityResults`, `ListExecutionIDsByLabels`, `BatchDeleteExecutions`, `RemoveLabelsFromExecutions`, `GetExecutionByID`, `ListLabelsByExecutionID`, `ListDetectorResultsByExecutionID`, `ListGranularityResultsByExecutionID`, `ListExecutions`, `ListExecutionLabels`, `ListLabelsGroupByCategory`, `AddExecutionLabels`, `ListLabelIDsByKeyAndExecutionID`, `ClearExecutionLabels`, `BatchDecreaseLabelUsages`, `GetProjectByName`, `UpdateExecution` |
| group.go | `GetTracesByGroupID`, `CountTracesByGroupID` |
| injection.go | `ListInjectionIDsByLabels`, `CreateInjection`, `AddInjectionLabels`, `UpdateGroundtruth`, `GetInjectionByID`, `ListLabelsByInjectionID`, `GetTaskByID`, `ListInjections`, `ListInjectionLabels`, `ExecuteSearch`, `ListLabelIDsByKeyAndInjectionID`, `ClearInjectionLabels`, `ListLabelIDsByConditions`, `ListLabelsByID`, `ListFaultInjectionsByID`, `ListInjectionsNoIssues`, `ListInjectionsWithIssues`, `GetProjectByName`, `GetHelmConfigByContainerVersionID`, `ListExistingEngineConfigs`, `ListExecutionsByDatapackIDs`, `BatchDeleteInjections` |
| label.go | `ListLabelsByID`, `ListContainerLabelCounts`, `RemoveContainersFromLabels`, `ListDatasetLabelCounts`, `RemoveDatasetsFromLabels`, `ListProjectLabelCounts`, `RemoveProjectsFromLabels`, `ListInjectionLabelCounts`, `RemoveInjectionsFromLabels`, `ListExecutionLabelCounts`, `RemoveExecutionsFromLabels`, `BatchUpdateLabels`, `BatchDeleteLabels`, `GetLabelByKeyAndValue`, `CreateLabel`, `UpdateLabel`, `GetLabelByID`, `RemoveContainersFromLabel`, `RemoveDatasetsFromLabel`, `RemoveProjectsFromLabel`, `RemoveInjectionsFromLabel`, `RemoveExecutionsFromLabel`, `BatchDecreaseLabelUsages`, `DeleteLabel`, `ListLabels` |
| metrics.go | (none — uses raw `database.DB` Find on `FaultInjection`, `Execution`, `Container`) |
| permission.go | `GetPermissionByActionAndResource`, `CheckUserHasPermission`, `GetPermissionByID`, `ListPermissions`, `ListRolesByPermissionID`, `ListPermissionsByID` |
| project.go | `GetRoleByName`, `CreateProject`, `CreateUserProject`, `RemoveUsersFromProject`, `DeleteProject`, `GetProjectByID`, `BatchGetProjectStatistics`, `GetProjectUserCount`, `ListProjects`, `ListProjectLabels`, `UpdateProject`, `AddProjectLabels`, `ListLabelIDsByKeyAndProjectID`, `ClearProjectLabels`, `ListLabelsByProjectID`, `ListProjectsByID`, `ListInjectionsByProjectID`, `ListExecutionsByProjectID`, `GetUserProjectRole`, `GetProjectTeamID` |
| rate_limiter.go | (none — direct redis `SMembers/SRem/Del/Scan` + `SELECT state` via `db.WithContext`) |
| relation.go | `GetUserByID`, `GetRoleByID`, `CreateUserRole`, `DeleteUserRole`, `BatchCreateUserPermissions`, `BatchDeleteUserPermisssions`, `BatchCreateRolePermissions`, `BatchDeleteRolePermisssions`, `ListUsersByRoleID`, `GetContainerByID`, `CreateUserContainer`, `DeleteUserContainer`, `GetDatasetByID`, `CreateUserDataset`, `DeleteUserDataset`, `GetProjectByID`, `CreateUserProject`, `DeleteUserProject` |
| resource.go | `GetResourceByID`, `ListResources`, `GetPermissionsByResource` |
| role.go | `CreateRole`, `GetRoleByID`, `RemoveContainersFromRole`, `RemoveDatasetsFromRole`, `RemoveProjectsFromRole`, `RemovePermissionsFromRole`, `RemoveUsersFromRole`, `DeleteRole`, `GetRoleUserCount`, `GetRolePermissions`, `ListRoles`, `UpdateRole` |
| sdk_evaluation.go | `ListSDKEvaluations`, `GetSDKEvaluationByID`, `ListSDKExperiments`, `ListSDKDatasetSamples` |
| spec_convert.go | (none) |
| system.go | `ListReadyTasks`, `ListDelayedTasks` |
| task.go | `GetTaskByID`, `UpdateTaskExecuteTime`, `ExpediteDelayedTask`, `BatchDeleteTasks`, `ListTasks` |
| team.go | `GetRoleByName`, `CreateTeam`, `CreateUserTeam`, `DeleteTeam`, `GetTeamByID`, `GetTeamUserCount`, `GetTeamProjectCount`, `ListUserTeamsByUserID`, `ListTeams`, `UpdateTeam`, `ListProjectsByTeamID`, `BatchGetProjectStatistics`, `GetUserByUsername`, `GetRoleByID`, `DeleteUserTeam`, `ListUsersByTeamID`, `GetUserTeamRole` |
| trace.go | `GetTraceByID`, `ListTraces` |
| upload.go | `GetInjectionByName` (also calls `producer.CreateInjection` → repo.CreateInjection) |
| user.go | `GetUserByID`, `GetUserByUsername`, `GetUserByEmail`, `CreateUser`, `RemoveContainersFromUser`, `RemoveDatasetsFromUser`, `RemoveProjectsFromUser`, `RemovePermissionsFromUser`, `RemoveRolesFromUser`, `DeleteUser`, `ListRolesByUserID`, `ListPermissionsByUserID`, `ListUsers`, `UpdateUser`, `IsSystemAdmin` |

`common/` → repository:

| file | repository.* |
|---|---|
| common/container.go | `BatchGetContainerVersions`, `CheckContainerExistsWithDifferentType`, `ListContainerVersionEnvVars`, `ListHelmConfigValues` (the latter two are `repository.ParameterConfigFetcher` callbacks invoked through `listParameterItems`) |
| common/dataset.go | `BatchGetDatasetVersions` |
| common/dynamic_config.go | `CreateConfig` |
| common/label.go | `ListLabelsByConditions`, `BatchIncreaseLabelUsages`, `BatchCreateLabels` |
| common/metadata_store.go | `GetSystemMetadata`, `ListServiceNames`, `ListSystemMetadata` |
| common/task.go | `GetParentTaskLevelByID`, `UpsertTrace`, `UpsertTask`, `SubmitImmediateTask`, `SubmitDelayedTask` |
| common/config_listener.go | `ListConfigByScope` |
| common/config_registry.go | `GetConfigByKey` |

`analyzer/evaluation.go` → repository: `ListExecutionsByDatapackFilter`, `ListExecutionsByDatasetFilter` (and uses `database.DB.Create(&evals)` directly to insert `database.Evaluation`).

---

## 3. Producer / common → client/* edge list

| caller file | client.* call (deduped) |
|---|---|
| producer/dynamic_config.go | `client.EtcdGet`, `client.EtcdPut`, `client.GetRedisClient` (subscribe to `consts.ConfigUpdateResponseChannel`) |
| producer/group.go | `client.RedisXRead` |
| producer/injection.go | `client.SetHashField` (key = `consts.InjectionAlgorithmsKey`) |
| producer/notification.go | `client.RedisXRead` |
| producer/rate_limiter.go | `client.GetRedisClient` (uses `Scan/SMembers/SRem/Del`) |
| producer/system.go | `client.GetRedisClient` (HGetAll on `consts.NamespaceKeyPattern`, ZAdd/ZRangeByScore/ZRemRangeByScore on `system:metrics:*`) |
| producer/task.go | `client.RedisXAdd` (stream `consts.StreamTraceLogKey`), `client.GetRedisClient` (Subscribe on `joblogs:<taskID>`), `client.NewLokiClient` + `(*LokiClient).QueryJobLogs`, `client.QueryOpts` |
| producer/injection.go | `client.NewLokiClient`, `(*LokiClient).QueryJobLogs` (for `GetInjectionLogs`) |
| producer/trace.go | `client.CheckCachedField`, `client.GetHashField`, `client.RedisXRead` |
| common/config_listener.go | `client.EtcdGet`, `client.EtcdPut`, `client.EtcdWatch` |
| common/config_registry.go | `client.RedisPublish` (channel `consts.ConfigUpdateResponseChannel`) |
| common/task.go | `client.RedisXAdd` (stream `consts.StreamTraceLogKey`) |

No direct calls observed into `client/k8s`, `client/harbor`, `client/jaeger`, `client/helm`, or `client/debug` from producer/common. Those packages are exercised by `service/consumer/*` (out of slice). The chaos system registration calls `chaos.RegisterSystem` directly (chaos_system.go:71,128,155), not via `client/*`.

---

## 4. Producer → service/common / service/consumer edges

`SubmitTask` (`common/task.go:51`) is the canonical async-enqueue API. No producer file imports `service/consumer`. The cross-service call graph inside the slice:

| producer caller | common.* function | what gets enqueued |
|---|---|---|
| injection.go:999 (`ProduceRestartPedestalTasks`) | `common.SubmitTask` | `dto.UnifiedTask{Type: TaskTypeRestartPedestal}`, payload includes pedestal/helm/inject batch (nodes or guidedConfigs) |
| injection.go:1095 (`ProduceDatapackBuildingTasks`) | `common.SubmitTask` | `TaskTypeBuildDatapack`, immediate |
| execution.go:357 (`ProduceAlgorithmExeuctionTasks`) | `common.SubmitTask` | `TaskTypeRunAlgorithm`, immediate |
| container.go:628 (`ProduceContainerBuildingTask`) | `common.SubmitTask` | `TaskTypeBuildContainer`, immediate |
| common/injection.go:28 (`ProduceFaultInjectionTasks`) | `common.SubmitTask` | `TaskTypeFaultInjection`, deferred to `injectTime` |

Other common usage from producer (helpers, not enqueue):

| producer caller | common.* used |
|---|---|
| injection.go (CreateInjection, ManageInjectionLabels, BatchManage…, ProduceRestartPedestalTasks) | `common.CreateOrUpdateLabelsFromItems`, `common.MapRefsToContainerVersions`, `common.ListContainerVersionEnvVars`, `common.ListHelmConfigValues` |
| execution.go | `common.MapRefsToContainerVersions`, `common.ListContainerVersionEnvVars`, `common.CreateOrUpdateLabelsFromItems` |
| container.go | `common.CreateOrUpdateLabelsFromItems` |
| dataset.go | `common.CreateOrUpdateLabelsFromItems` |
| project.go | `common.CreateOrUpdateLabelsFromItems` |
| common.go | `common.MapRefsToDatasetVersions` |
| dynamic_config.go | `common.ValidateConfig`, `common.ValidateConfigMetadataConstraints` |
| analyzer/evaluation.go | `common.MapRefsToContainerVersions`, `common.MapRefsToDatasetVersions` |

### `SubmitTask` chain detail (common/task.go:51)

1. Generates UUIDs for `TraceID`/`TaskID` if absent.
2. If `ParentTaskID != nil` and not `TaskRescheduled`, fetches parent level via `repository.GetParentTaskLevelByID` and bumps `t.Level`.
3. If not `Immediate`, calls `calculateExecuteTime` (cron next time for `TaskTypeCronJob`).
4. If root task (`ParentTaskID == nil`), constructs a `database.Trace` via `t.ConvertToTrace(...)`. For `TaskTypeRestartPedestal` reads `consts.TaskExtraInjectionAlgorithms` from `t.Extra` to derive `leafNum`.
5. Inside a single GORM transaction: `repository.UpsertTrace` (if any) then `repository.UpsertTask`.
6. Marshals task to JSON, then routes:
   - `Immediate`: `repository.SubmitImmediateTask(ctx, taskData, t.TaskID)`
   - else: `repository.SubmitDelayedTask(ctx, taskData, t.TaskID, t.ExecuteTime)` followed by `EmitTaskScheduled` which writes a `consts.EventTaskScheduled` event onto the per-trace stream `fmt.Sprintf(consts.StreamTraceLogKey, t.TraceID)` via `client.RedisXAdd` (task.go:158).

The Redis "where" therefore lives in `repository.SubmitImmediateTask/SubmitDelayedTask` (out-of-slice) and `consts.StreamTraceLogKey` for the trace event ledger.

---

## 5. Producer → utils / dto / chaos-experiment / external edges (deduped)

Non-stdlib, non-stdlib-database imports invoked by producer/common functions:

- `aegis/dto` — pervasive; every file constructs `dto.NewXxxResp`/`dto.UnifiedTask`/`dto.LabelItem` etc. (not enumerated)
- `aegis/utils`:
  - `utils.HashPassword`, `utils.VerifyPassword`, `utils.GenerateToken`, `utils.ValidateToken`, `utils.Claims` (auth.go)
  - `utils.IntPtr`, `utils.StringPtr`, `utils.GetIntValue`, `utils.ToUniqueSlice`, `utils.ConvertSimpleTypeToString`, `utils.ConvertStringToSimpleType` (multiple)
  - `utils.GenerateColorFromKey` (common/label.go)
  - `utils.IsAllowedPath`, `utils.AddToZip`, `utils.MatchFile`, `utils.ExculdeRule` (dataset.go, injection.go)
  - `utils.CalculateFileSHA256`, `utils.CopyFileFromFileHeader`, `utils.CopyFile`, `utils.CopyDir` (container.go)
- `aegis/config`: `config.GetString`, `config.GetDetectorName`, `config.SetDetectorName`, `config.SetViperValue` (used in dynamic_config and many service files for jfs paths / harbor registry / detector name)
- `aegis/consts`: enums and Redis key templates throughout
- `aegis/database`: `database.DB`, model types (`FaultInjection`, `Task`, `Trace`, `Execution`, `Label`, `Container`, etc.), `Groundtruth`, `Evaluation` create
- `github.com/OperationsPAI/chaos-experiment/handler` (alias `chaos`):
  - `chaos.SystemType`, `chaos.ChaosType`, `chaos.Node`, `chaos.NodeToStruct[InjectionConf]`, `chaos.InjectionConf.GetGroundtruth`, `chaos.GetAllSystemTypes`, `chaos.RegisterSystem`, `chaos.UnregisterSystem`, `chaos.SystemConfig`, `chaos.ChaosTypeMap`, `chaos.SpecMap`, `chaos.Groundtruth`, `chaos.MetadataStore` interface impls (ServiceEndpointData, JavaClassMethodData, DatabaseOperationData, GRPCOperationData, NetworkPairData, RuntimeMutatorTargetData)
- `github.com/OperationsPAI/chaos-experiment/pkg/guidedcli`:
  - `guidedcli.GuidedConfig`, `guidedcli.BuildInjection` (injection.go:1246) — **the guided-CLI integration entrypoint**
- `github.com/redis/go-redis/v9`: `redis.Nil`, `redis.Z`, `redis.ZRangeBy`, `redis.XStream`, `redis.XMessage`, `redis.Client`, `redis.Message`
- `github.com/gorilla/websocket`: `websocket.Conn`, `websocket.PingMessage`, `websocket.CloseMessage`, `websocket.IsUnexpectedCloseError`, `websocket.FormatCloseMessage` (task.go log streamer)
- `github.com/sirupsen/logrus`: pervasive structured logging
- `gorm.io/gorm`: `gorm.DB`, `gorm.ErrRecordNotFound`, `gorm.ErrDuplicatedKey` everywhere
- `golang.org/x/crypto/bcrypt`: `bcrypt.GenerateFromPassword`, `bcrypt.DefaultCost` (user.go)
- `github.com/google/uuid`: `uuid.NewString` (common/task.go:53,57)
- `github.com/robfig/cron/v3`: `cron.Parse` for `CronNextTime` (common/task.go:23)
- `go.etcd.io/etcd/client/v3`: `clientv3.Event` (common/config_listener.go:191)
- `github.com/shirou/gopsutil/v3/{cpu,mem,disk}`: host metric collection (system.go:120,130,136)
- `github.com/apache/arrow-go/v18/arrow/ipc` + `github.com/duckdb/duckdb-go/v2`: DuckDB → Arrow IPC parquet streamer (query_datapack_arrow.go) — **only loaded under `duckdb_arrow` build tag**
- No OpenTelemetry references in producer/common slice; tracing-context plumbing happens via `dto.UnifiedTask.SetGroupCtx(ctx)` and the consumer side.

---

## 6. service/common internals — recap

- **`metadata_store.go`** — `DBMetadataStore` (sync.Map cache) implements `chaos.MetadataStore` (metadata_store.go:15). Only consumed by chaos-experiment; not reached from producer directly.
- **`template.go`** — internal `extractTemplateVars` + `renderTemplate` for `{{ .Field }}` substitution against a context struct (template.go:11,31). Used by `processParameterConfig` in `container.go`.
- **`config_listener.go`** — Singleton `configUpdateListener` with `EnsureScope(scope)` API (config_listener.go:66). Maintains `active map[ConfigScope]bool`; per scope: `loadScopeFromEtcd` (seeds etcd with MySQL defaults), then `watchPrefix` goroutine routes events to `handleConfigChange` in `config_registry.go`. Producer scope has no etcd prefix and is silently skipped.
- **`config_registry.go`** — `configRegistry` keyed by `(scope, category)`. `RegisterHandler` plug-in API (config_registry.go:44); `RegisterGlobalHandlers` registers `algoConfigHandler` (handles `consts.DetectorKey` → `config.SetDetectorName`). `PublishWrapper` wraps a handler closure and emits `dto.ConfigUpdateResponse` on `consts.ConfigUpdateResponseChannel` via `client.RedisPublish` so producers awaiting consumer ack can unblock.
- **`dataset.go`** — `MapRefsToDatasetVersions` (dataset.go:11). Resolves `[]*dto.DatasetRef` → `database.DatasetVersion` via batch query, picks first version when ref omits `Version`.
- **`injection.go`** — `ProduceFaultInjectionTasks` (injection.go:13): single-purpose helper; constructs `dto.UnifiedTask{Type: TaskTypeFaultInjection, Immediate: false, ExecuteTime: injectTime.Unix()}`, propagates `TraceCarrier`/`GroupCarrier`, calls `SubmitTask`. **Note**: not currently called from any producer file in the slice — the producer-side fault-injection batches go through `ProduceRestartPedestalTasks` which submits a `TaskTypeRestartPedestal`. This helper is reachable only by consumer (out of slice) — see surprises §9.
- **`container.go`** — three exports: `ListContainerVersionEnvVars`, `ListHelmConfigValues`, `MapRefsToContainerVersions`. Internally `processParameterConfig` handles `Fixed`/`Dynamic` (template) parameter types, validating required fields and rendering via `renderTemplate`.
- **`label.go`** — `CreateOrUpdateLabelsFromItems` (label.go:34): finds existing labels via `repository.ListLabelsByConditions`, bumps usage on existing ones via `BatchIncreaseLabelUsages`, batch-creates missing ones via `BatchCreateLabels`. Drives the label dedup contract used everywhere.
- **`task.go`** — `SubmitTask` (canonical enqueue, see §4 detail) + `EmitTaskScheduled` (writes `task.scheduled` event to per-trace stream).
- **`dynamic_config.go`** — `CreateConfig` thin wrapper + `ValidateConfig`/`ValidateConfigMetadataConstraints`. Validation rule table at `configTypeRules` (dynamic_config.go:25) keyed by `consts.ConfigValueType`.

### Is `SubmitTask` the canonical enqueue API?

**Yes** within the slice. Every async-task-enqueue from `service/producer/*` and `service/common/*` flows through `common.SubmitTask`. Producers either call it directly (container.go:628, execution.go:357, injection.go:999, injection.go:1095) or via the wrapper `common.ProduceFaultInjectionTasks`. The Redis push happens in `repository.SubmitImmediateTask` / `repository.SubmitDelayedTask` (out-of-slice). The trace event bus is `client.RedisXAdd` on `fmt.Sprintf(consts.StreamTraceLogKey, traceID)` (common/task.go:158).

---

## 6b. service/analyzer/evaluation.go

`ListDatapackEvaluationResults(req, userID)` and `ListDatasetEvaluationResults(req, userID)`:

- Inputs: `dto.BatchEvaluateDatapackReq` / `dto.BatchEvaluateDatasetReq` containing pairs `(Algorithm ContainerRef, Datapack | Dataset, FilterLabels)`.
- Resolves algorithms with `common.MapRefsToContainerVersions` and (for dataset variant) datasets with `common.MapRefsToDatasetVersions`.
- Queries `repository.ListExecutionsByDatapackFilter` / `ListExecutionsByDatasetFilter` per spec, builds `dto.EvaluateDatapackRef` (or `EvaluateRefs` grouped by datapack name) with one `ExecutionRef` per execution. Reads `groundtruths` from `database.FaultInjection.Groundtruths` via `getGroundtruths` (which round-trips each `database.Groundtruth` through `ConvertToChaosGroundtruth` to produce `chaos.Groundtruth`).
- For dataset variant computes `NotExecutedDatapacks = datasetVersion.Datapacks − executed`.
- `persistEvaluations[T]` (generic, evaluation.go:245): batch-inserts `database.Evaluation` rows directly via `database.DB.Create(&evals)` with `ResultJSON = json.Marshal(item)`. Failures are logged, not propagated.

Callers in slice: none — these are called from `handlers/v2/*`. Within the slice, this file is only an analyzer entrypoint sitting alongside producer.

---

## 7. Datapack / Dataset build pipeline (the critical async path)

Functions involved (slice only):

| step | function | side effects |
|---|---|---|
| 1. inject batch produced | `producer.ProduceRestartPedestalTasks` (injection.go:806) | resolves pedestal+benchmark via `MapRefsToContainerVersions`, renders helm values via `ListHelmConfigValues`, parses each batch with either `parseBatchInjectionSpecs` (legacy `chaos.Node`) or `parseBatchGuidedSpecs` (calls `guidedcli.BuildInjection`). Stores algorithm versions for the group via `client.SetHashField(InjectionAlgorithmsKey, groupID, items)`. Submits one `TaskTypeRestartPedestal` per de-duplicated batch with `Extra[TaskExtraInjectionAlgorithms] = len(req.Algorithms)`. Engine-config dedup uses `repository.ListExistingEngineConfigs` (batch size 100). |
| 2. CRD success → fault injection enqueue | `common.ProduceFaultInjectionTasks` (common/injection.go:13) | creates child `TaskTypeFaultInjection`. (Caller is in consumer; surfaced here because the function lives in slice.) |
| 3. build datapack enqueue | `producer.ProduceDatapackBuildingTasks` (injection.go:1023) | per spec calls `extractDatapacks` (common.go:38) which uses `repository.GetInjectionByName` or `repository.ListInjectionsByDatasetVersionID` and validates state against `taskTypeDatapackStates[TaskTypeBuildDatapack] = {InjectSuccess, BuildFailed, BuildSuccess, DetectorFailed, DetectorSuccess}`. Submits one `TaskTypeBuildDatapack` per datapack with `Immediate: true` and payload `{Benchmark, Datapack, DatasetVersionID, Labels}`. |
| 4. algo execution enqueue | `producer.ProduceAlgorithmExeuctionTasks` (execution.go:291) | analogous; uses `taskTypeDatapackStates[TaskTypeRunAlgorithm] = {DetectorSuccess}`; refuses `consts.LabelKeyTag = consts.DetectorNoAnomaly` (common.go:55); submits `TaskTypeRunAlgorithm`. |
| 5. zipping for download | `producer.DownloadDatapack` → `packageDatapackToZip` (injection.go:595) | walks `filepath.Join(config.GetString("jfs.dataset_path"), datapack.Name)` (injection.go:600), respects `excludeRules` via `utils.MatchFile`, writes via `utils.AddToZip` into `consts.DownloadFilename/<base>/<rel>`. |
| 6. file tree | `producer.GetDatapackFiles` → `buildFileTree` (injection.go:1448) | reads same `jfs.dataset_path/<name>` directory to populate `dto.DatapackFilesResp`. |
| 7. parquet streaming | `producer.QueryDatapackFileContent` (query_datapack_arrow.go:19) | requires `.parquet`; opens DuckDB in-memory connector, casts UINT64/UHUGEINT → BIGINT for Arrow IPC compatibility (`buildSafeParquetSQL`), streams via `arrow/ipc.NewWriter` with Zstd. |
| 8. manual upload | `producer.UploadDatapack` (upload.go:33) | extracts zip into `<jfs.dataset_path>/<req.Name>`, validates ≥1 of the six known parquet filenames (`abnormal_traces.parquet`, …, `normal_logs.parquet`), parses ground-truth from request or from `injection.json` → creates `database.FaultInjection{Source: DatapackSourceManual, State: DatapackBuildSuccess}` via `CreateInjection` (injection.go:79). |
| evaluation | `analyzer.ListDataset/DatapackEvaluationResults` | reads `Datapack.Groundtruths` and persists `database.Evaluation` rows. |

**Filesystem paths** referenced (always under `config.GetString("jfs.dataset_path")`):
- `<dataset_path>/<datapack.Name>/...` — datapack files (parquet, injection.json, etc.)
- `<dataset_path>/helm-charts/<container>_chart_<ts><ext>` — uploaded helm charts (container.go:480)
- `<dataset_path>/helm-values/<container>_values_<ts><ext>` — uploaded helm value files (container.go:549)
- `<jfs.container_path>/<image>/build_<ts>/...` — git-cloned container source (container.go:817)

---

## 8. Rate limiter subsystem (producer/rate_limiter.go)

- Redis key prefix: `tokenBucketKeyPrefix = "token_bucket:"` (rate_limiter.go:18).
- Known buckets and capacities (`knownBuckets()`, rate_limiter.go:20):
  - `consts.RestartPedestalTokenBucket` → `consts.MaxConcurrentRestartPedestal`
  - `consts.BuildContainerTokenBucket` → `consts.MaxConcurrentBuildContainer`
  - `consts.AlgoExecutionTokenBucket` → `consts.MaxConcurrentAlgoExecution`
- Semaphore semantics: bucket = Redis **set** (`SADD`/`SREM`/`SMEMBERS`). Held tokens are taskIDs (members). Capacity is enforced **outside** this file (consumer-side acquire path); producer only inspects, resets, and garbage-collects. There is no `INCR/DECR` counter — `len(SMEMBERS) ≤ capacity` is the invariant.
- `ListRateLimiters(ctx)` — `SCAN token_bucket:*`, then `SMEMBERS` per bucket; for each holder taskID, looks up `database.Task.state` to flag terminal holders for the UI (rate_limiter.go:36).
- `ResetRateLimiter(ctx, bucket)` — `DEL token_bucket:<bucket>` (rate_limiter.go:88). Rejects unknown buckets.
- `GCRateLimiters(ctx)` — entry point at rate_limiter.go:106 → testable core `gcRateLimitersWith` (rate_limiter.go:111). For each known bucket: `SMEMBERS` → for each taskID look up state via `lookupTaskStateWith` (`SELECT state FROM tasks WHERE id=?`, rate_limiter.go:154). If task missing OR `isTerminalState` (`TaskCompleted | TaskError | TaskCancelled`), `SREM` it; returns `(released, touchedBuckets, err)`.
- Note `isTerminalState` (rate_limiter.go:31) and `task.go:isTaskTerminal` (task.go:441) are duplicates with the same semantics — see surprises.

---

## 9. Surprises / dead code

1. **`common.ProduceFaultInjectionTasks` has no caller in slice.** It enqueues `TaskTypeFaultInjection` but no producer file calls it. Likely invoked from `service/consumer/*` (CRD callback path); within the synchronous service layer it is dormant.
2. **Two `isTaskTerminal` impls.** `producer/task.go:441` and `producer/rate_limiter.go:31` both define `state ∈ {Completed, Error, Cancelled}`. Comment at rate_limiter.go:28 explicitly mirrors the contract — clear refactor candidate.
3. **`producer.SearchUsers`** (user.go:206) is a stub returning `(nil, nil)`. Either dead or shimmed for an unimplemented endpoint.
4. **`init()` in `producer/system.go:269`** spawns a 1-minute metric-collection goroutine on package import. Side-effecting init — easy to miss. Failures are silently dropped (`runtime.Gosched()` on error, system.go:279).
5. **`ManageProjectLabels` / `ManageExecutionLabels` removal logic is inverted.** Both branches at project.go:237 and execution.go:263 only call `ClearProjectLabels`/`ClearExecutionLabels` when `len(labelIDs) == 0`; the non-empty branch is unreachable. By contrast `ManageContainerLabels` (container.go:248) and `ManageInjectionLabels` (injection.go:578) do it correctly. Looks like a copy-paste regression.
6. **`GetAlgorithmMetrics` hard-codes container type 2** (metrics.go:190 `Where("type = ?", 2)`) and does not route through `repository.*`. State 2/3 numeric literals are also used directly (metrics.go:78,80,158,160,239,241) bypassing `consts.ExecutionState`/`consts.DatapackState`.
7. **`processGitHubSource` branch bug** (container.go:828-832) — when `req.GithubBranch` is empty the code appends `[repoURL, targetDir]`, but when non-empty it appends `--branch <branch> --single-branch ...`. The else branch logic is inverted relative to the comment intent — non-branch case clones full repo, branch case clones single branch only when also a commit is requested. Worth flagging.
8. **`extractGroundtruthFromInjectionJSON`** (upload.go:225) accepts both `ground_truths` and `ground_truth` keys (upload.go:240) — schema drift compensation.
9. **Engine-config dedup uses raw JSON marshal of either `nodes` or `guidedConfigs`** (injection.go:1331-1351). Two batches with semantically equal but textually different ordering would not dedup; sorting only happens for legacy `nodes` via `sortNodes` (injection.go:1414).
10. **`producer/dynamic_config.go` carries its own `etcdPrefixForScope`** (dynamic_config.go:52) duplicating the `scopePrefix` map in `common/config_listener.go:21`. Same data, two sources of truth.
