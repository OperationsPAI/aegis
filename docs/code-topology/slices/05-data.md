# 05 — Data Layer (repository / database / dto / consts)

Sources audited (.go only): `src/database/{database,entity,entity_helper,scope,sdk_entities,view}.go`,
`src/repository/*.go` (24 files), `src/dto/*.go` (35 files), `src/consts/*.go` (7 files).

Notes on conventions:
- All entities use `consts.StatusType` for soft-delete via integer flag (`-1=deleted, 0=disabled, 1=enabled`); GORM `gorm.DeletedAt` is **NOT** used anywhere (custom soft-delete pattern).
- Many entities expose a `Active*` virtual column (`GENERATED ALWAYS AS … VIRTUAL`) plus a unique index, to enforce uniqueness only over non-deleted rows.
- DB driver is MySQL only (`database/database.go:42`); GORM is wrapped with the OpenTelemetry tracing plugin (`database.go:142`).

## 1. Entity inventory

All entries below are in `src/database/entity.go` unless noted. Soft-delete column = `Status consts.StatusType` (no entity uses `gorm.DeletedAt`).

| Struct | Table (default snake-plural) | Key fields / indices / unique | FK relationships | Soft-delete |
|---|---|---|---|---|
| DynamicConfig (`entity.go:19`) | `dynamic_configs` | `config_key+category` idx, JSON `Options`, `IsSecret` | FK `UpdatedBy → User`; M2M `Labels` via `config_labels` | yes |
| ConfigHistory (`entity.go:49`) | `config_histories` | `ConfigID` idx; tracks operator/IP/UA | FK `ConfigID → DynamicConfig`, `OperatorID → User`, `RolledBackFromID → ConfigHistory` (self) | no Status |
| Container (`entity.go:75`) | `containers` | `Name+Type`; virtual `ActiveName` unique idx; status idx | hasMany `Versions`; M2M `Labels` via `container_labels`; `BeforeCreate` validates pedestal `chaos.SystemType` | yes |
| ContainerVersion (`entity.go:103`) | `container_versions` | semver tri-index `idx_container_version_name_order`; virtual `ActiveVersionKey` unique; `usage_count check>=0` | FK `ContainerID, UserID`; one-to-one `HelmConfig`; M2M `EnvVars (ParameterConfig via container_version_env_vars)` | yes |
| HelmConfig (`entity.go:175`) | `helm_configs` | `ContainerVersionID` unique | FK `ContainerVersionID` (CASCADE); M2M `DynamicValues (ParameterConfig via helm_config_values)` | none |
| ParameterConfig (`entity.go:212`) | `parameter_configs` | composite unique `(config_key,type,category)`; `BeforeCreate` validates env-var/helm-value key | none direct; reverse via M2M | none |
| ContainerVersionEnvVar (`entity.go:251`) | `container_version_env_vars` | composite PK `(ContainerVersionID, ParameterConfigID)` | FKs to both | none |
| HelmConfigValue (`entity.go:262`) | `helm_config_values` | composite PK `(HelmConfigID, ParameterConfigID)` | FKs to both | none |
| Dataset (`entity.go:272`) | `datasets` | virtual `ActiveName` unique; status idx | hasMany `Versions`; M2M `Labels` via `dataset_labels` | yes |
| DatasetVersion (`entity.go:290`) | `dataset_versions` | semver tri-index; virtual `ActiveVersionKey` unique | FK `DatasetID, UserID`; M2M `Datapacks (FaultInjection via dataset_version_injections)` | yes |
| Project (`entity.go:329`) | `projects` | virtual `ActiveName` unique; team-idx | FK `TeamID`; M2M `Containers, Datasets, Labels` | yes |
| Label (`entity.go:351`) | `labels` | composite idx `(label_key, category)`; virtual `ActiveKeyValue` unique; `usage_count check>=0` | none direct; many M2M targets reference it | yes |
| Team (`entity.go:369`) | `teams` | virtual `ActiveName` unique | hasMany `Projects` | yes |
| User (`entity.go:386`) | `users` | unique `Username`, `Email`; virtual `ActiveUsername` unique | `BeforeCreate` hashes password | yes |
| Role (`entity.go:416`) | `roles` | virtual `ActiveName` unique; `IsSystem` flag | none direct | yes |
| Permission (`entity.go:431`) | `permissions` | composite idx `(action, scope, resource_id)`; virtual `ActiveName` unique | FK `ResourceID → Resource` | yes |
| Resource (`entity.go:452`) | `resources` | unique `Name`; composite `(category, parent_id)` | FK `Parent → Resource` (self) | none (no Status) |
| AuditLog (`entity.go:468`) | `audit_logs` | composite `(action,state)`, `(user_id,created_at)`; `ResourceID` idx | FK `UserID → User`, `ResourceID → Resource` | yes |
| Trace (`entity.go:495`) | `traces` | string PK; `(type,state)` idx, `(project_id,state)` idx; `GroupID` idx | FK `ProjectID`; hasMany `Tasks` | yes |
| Task (`entity.go:519`) | `tasks` | string PK; composite `(type,status)`/`(type,state)`; `TraceID` idx; `ParentTaskID` idx | FK `TraceID → Trace` (CASCADE); `ParentTaskID → Task` (CASCADE, self); reverse one-to-one `FaultInjection`, `Execution` (CASCADE on delete); hasMany `SubTasks` | yes |
| FaultInjection (`entity.go:550`) | `fault_injections` | composite `(fault_type, state)`; `(benchmark_id, pedestal_id)`; `TaskID` idx; `Groundtruths` JSON via `Groundtruth.Scan/Value` (`entity_helper.go:37,51`); `start_time<end_time` check | FK `BenchmarkID → ContainerVersion` (SET NULL), `PedestalID → ContainerVersion` (SET NULL), `TaskID → Task` (CASCADE); M2M `Labels` via `fault_injection_labels` | yes |
| Execution (`entity.go:582`) | `executions` | composite `(algorithm_version_id, datapack_id)`; `TaskID` idx | FK `TaskID → Task` (CASCADE), `AlgorithmVersionID → ContainerVersion` (RESTRICT), `DatapackID → FaultInjection` (RESTRICT), `DatasetVersionID → DatasetVersion` (SET NULL); hasMany `DetectorResults`, `GranularityResults`; M2M `Labels` via `execution_injection_labels` | yes |
| DetectorResult (`entity.go:608`) | `detector_results` | float metrics for normal/abnormal periods; per-`SpanName` rows | FK `ExecutionID → Execution` (CASCADE) | none |
| GranularityResult (`entity.go:628`) | `granularity_results` | `Level` (service/pod/span/metric), `Rank`, `Confidence`; `ExecutionID` idx | FK `ExecutionID → Execution` (CASCADE) | none |
| System (`entity.go:645`) | `systems` | unique `Name`; `IsBuiltin`; status idx | none | yes |
| SystemMetadata (`entity.go:660`) | `system_metadata` | composite idx `(system_name, metadata_type, service_name)`; `Data json` | none | none |
| ContainerLabel (`entity.go:675`) | `container_labels` | composite PK | FKs both | — |
| DatasetLabel (`entity.go:686`) | `dataset_labels` | composite PK | FKs both | — |
| ProjectLabel (`entity.go:697`) | `project_labels` | composite PK | FKs both | — |
| DatasetVersionInjection (`entity.go:708`) | `dataset_version_injections` | composite PK | FK `DatasetVersion`, `FaultInjection` | — |
| FaultInjectionLabel (`entity.go:719`) | `fault_injection_labels` | composite PK | FK `FaultInjection` (CASCADE) | — |
| ExecutionInjectionLabel (`entity.go:729`) | `execution_injection_labels` | composite PK | FK `Execution` (CASCADE), `Label` | — |
| ConfigLabel (`entity.go:740`) | `config_labels` | composite PK | FK `DynamicConfig`, `Label` | — |
| UserContainer (`entity.go:751`) | `user_containers` | virtual `ActiveUserContainer` unique on `(user_id,container_id,role_id)` | FK `User, Container, Role` | yes |
| UserDataset (`entity.go:769`) | `user_datasets` | virtual `ActiveUserDataset` unique | FK `User, Dataset, Role` | yes |
| UserProject (`entity.go:788`) | `user_projects` | virtual `ActiveUserProject` unique; carries `WorkspaceConfig` JSON | FK `User, Project, Role` | yes |
| UserTeam (`entity.go:809`) | `user_teams` | virtual `ActiveUserTeam` unique | FK `User, Team, Role` | yes |
| UserRole (`entity.go:828`) | `user_roles` | composite unique `(UserID, RoleID)` | FK `User, Role` | none Status |
| RolePermission (`entity.go:842`) | `role_permissions` | composite unique `(RoleID, PermissionID)` | FK `Role, Permission` | none Status |
| Evaluation (`entity.go:856`) | `evaluations` | EvalType=`datapack|dataset`; precision/recall/f1/accuracy floats | FK `ProjectID` (nullable) | yes |
| UserPermission (`entity.go:876`) | `user_permissions` | three composite uniques per scope (`container/dataset/project`); `ExpiresAt`; `GrantType` (grant/deny) | FK `User, Permission, Container, Dataset, Project` | none Status |

Defined elsewhere:
- `Groundtruth` (`entity_helper.go:12`) — embedded JSON column on `FaultInjection`; mirrors `chaos.Groundtruth`; implements `sql.Scanner` + `driver.Valuer`.
- `SDKDatasetSample` (`sdk_entities.go:7`) → table `data` (read-only; `TableName()` override; **excluded from AutoMigrate**).
- `SDKEvaluationSample` (`sdk_entities.go:26`) → table `evaluation_data` (read-only; excluded from AutoMigrate).
- View entities `FaultInjectionNoIssues` (`view.go:12`) and `FaultInjectionWithIssues` (`view.go:28`) — backed by SQL views created in `createDetectorViews()` (`view.go:69`).

`AutoMigrate` set is enumerated in `database/database.go:61-113`. SDK + View structs are deliberately omitted.

## 2. Entity relationship graph (FK + M2M)

Direct FKs (`A --FK:col--> B`):

```
ConfigHistory  --FK:config_id-->          DynamicConfig
ConfigHistory  --FK:operator_id-->        User
ConfigHistory  --FK:rolled_back_from_id--> ConfigHistory (self)
DynamicConfig  --FK:updated_by-->         User

ContainerVersion --FK:container_id-->     Container
ContainerVersion --FK:user_id-->          User
HelmConfig       --FK:container_version_id (1:1, CASCADE)--> ContainerVersion
DatasetVersion   --FK:dataset_id-->       Dataset
DatasetVersion   --FK:user_id-->          User

Project          --FK:team_id-->          Team
Permission       --FK:resource_id-->      Resource
Resource         --FK:parent_id-->        Resource (self)
AuditLog         --FK:user_id-->          User
AuditLog         --FK:resource_id-->      Resource

Trace            --FK:project_id-->       Project
Task             --FK:trace_id (CASCADE)--> Trace
Task             --FK:parent_task_id (CASCADE, self)--> Task
FaultInjection   --FK:benchmark_id (SET NULL)--> ContainerVersion
FaultInjection   --FK:pedestal_id  (SET NULL)--> ContainerVersion
FaultInjection   --FK:task_id (CASCADE)--> Task
Execution        --FK:task_id (CASCADE)--> Task
Execution        --FK:algorithm_version_id (RESTRICT)--> ContainerVersion
Execution        --FK:datapack_id (RESTRICT)--> FaultInjection
Execution        --FK:dataset_version_id (SET NULL)--> DatasetVersion
DetectorResult   --FK:execution_id (CASCADE)--> Execution
GranularityResult --FK:execution_id (CASCADE)--> Execution

UserContainer    --FK:(user_id, container_id, role_id) --> User, Container, Role
UserDataset      --FK:(user_id, dataset_id, role_id)   --> User, Dataset, Role
UserProject      --FK:(user_id, project_id, role_id)   --> User, Project, Role
UserTeam         --FK:(user_id, team_id, role_id)      --> User, Team, Role
UserRole         --FK:(user_id, role_id)               --> User, Role
RolePermission   --FK:(role_id, permission_id)         --> Role, Permission
UserPermission   --FK:(user_id, permission_id, ?container_id, ?dataset_id, ?project_id) --> User, Permission, optional scopes
Evaluation       --FK:project_id (nullable)            --> Project
```

M2M (explicit join tables):

```
container_labels        : Container        <-> Label
dataset_labels          : Dataset          <-> Label
project_labels          : Project          <-> Label
fault_injection_labels  : FaultInjection   <-> Label   (FaultInjection side CASCADE)
execution_injection_labels : Execution     <-> Label   (Execution side CASCADE)
config_labels           : DynamicConfig    <-> Label
container_version_env_vars : ContainerVersion <-> ParameterConfig
helm_config_values      : HelmConfig       <-> ParameterConfig
dataset_version_injections : DatasetVersion <-> FaultInjection
project_containers      : Project <-> Container         (implicit gorm M2M, declared only as relation)
project_datasets        : Project <-> Dataset           (implicit gorm M2M)
```

## 3. Repository file → entity mapping

| Repo file | Primary entities | Exported functions | Unusual queries |
|---|---|---|---|
| `audit.go` | AuditLog | CreateAuditLog, GetAuditLogByID, ListAuditLogs, GetAuditLogStatistics | Preload(User,Resource); aggregate Group by status/action (`audit.go:91-115`) |
| `common.go` | — | (consts only: `commonOmitFields="active_name"`) | — |
| `container.go` | Container, ContainerVersion, HelmConfig, ParameterConfig, ContainerLabel, ContainerVersionEnvVar, HelmConfigValue | Create/Delete/GetContainerByID, GetContainerStatistics, ListContainers, ListContainersByID, UpdateContainer, BatchCreate/Delete/GetContainerVersions, CheckContainerExistsWithDifferentType, DeleteContainerVersion, GetContainerVersionByID, ListContainerVersions(ByContainerID), UpdateContainerVersion, UpdateContainerVersionImageColumns, BatchCreateHelmConfigs, GetHelmConfigByContainerVersionID, UpdateHelmConfig, BatchCreateOrFindParameterConfigs, ListParameterConfigsByKeys, AddContainerLabels, ClearContainerLabels, RemoveContainersFromLabel(s), ListContainerLabels, ListContainerLabelCounts, ListLabelsByContainerID, ListLabelIDsByKeyAndContainerID, AddContainerVersionEnvVars, ListContainerVersionEnvVars, AddHelmConfigValues, ListHelmConfigValues | OnConflict upserts (`container.go:357,546,579`); uses local `containerVersionOmitFields="active_version_key,HelmConfig,EnvVars"` (`container.go:15`) |
| `dataset.go` | Dataset, DatasetVersion, DatasetLabel, DatasetVersionInjection | Create/Delete/Get/ListDatasets, ListDatasetsByID, UpdateDataset, GetDatasetStatistics, BatchCreate/Delete/GetDatasetVersions, DeleteDatasetVersion, GetDatasetVersionByID, ListDatasetVersions(ByDatasetID), UpdateDatasetVersion, AddDatasetLabels, ClearDatasetLabels, RemoveLabelsFromDataset, RemoveDatasetsFromLabel(s), ListDatasetLabelCounts, ListDatasetLabels, ListLabelsByDatasetID, ListLabelIDsByKeyAndDatasetID, AddDatasetVersionInjections, ClearDatasetVersionInjections, RemoveInjectionsFromDatasetVersion, RemoveDatasetVersionsFromInjection, ListInjectionsByDatasetVersionID | OnConflict upserts (`dataset.go:265,423`); embedded JOIN to fetch label map (`dataset.go:354+`) |
| `detector.go` | DetectorResult | ListDetectorResultsByExecutionID, SaveDetectorResults | trivial Where/Create |
| `dynamic_config.go` | DynamicConfig, ConfigHistory | Create/Get(ByKey/ID)/List(Existing/ByScope)/UpdateConfig, Create/Get(Latest)/List(History/ByConfigID) | Preload UpdatedByUser/Operator/Config |
| `evaluation.go` | Evaluation | List/Get/Create/DeleteEvaluation | trivial; soft-delete via status field |
| `execution.go` | Execution, ExecutionInjectionLabel | BatchDelete/Create/Get/UpdateExecution, ListExecutions, ListExecutionsByDatapackIDs, AddExecutionLabels, ClearExecutionLabels, RemoveLabelsFromExecution(s), RemoveExecutionsFromLabel(s), ListExecutionsByDatapackFilter, ListExecutionsByDatasetFilter, ListExecutionIDsByLabels, ListExecutionLabels, ListExecutionLabelCounts, ListLabelsByExecutionID, ListLabelIDsByKeyAndExecutionID, GetExecutionStatistics, ListExecutionsByProjectID | Heavy `Preload(DetectorResults, GranularityResults, AlgorithmVersion.Container, Datapack, DatasetVersion, DatasetVersion.Injections)` (`execution.go:233-283`); join `fault_injections` and `labels` with HAVING COUNT to AND-match label sets; OnConflict upsert on labels (`execution.go:153`) |
| `granularity.go` | GranularityResult | ListGranularityResultsByExecutionID, SaveGranularityResults | per-row Create with `Omit(containerVersionOmitFields)` (oddly cross-uses container constant — see Surprises) |
| `injection.go` | FaultInjection, FaultInjectionLabel, FaultInjectionNoIssues/WithIssues views | BatchDeleteInjections, CreateInjection, GetInjectionByID, GetInjectionByName, ListFaultInjectionsByID, ListExistingEngineConfigs, ListEngineConfigByNames, ListInjections, ListInjectionIDsByNames, UpdateGroundtruth, UpdateInjection, ListInjectionsNoIssues, ListInjectionsWithIssues, AddInjectionLabels, ClearInjectionLabels, RemoveInjectionsFromLabel(s), RemoveLabelsFromInjection(s), ListInjectionIDsByLabels, ListInjectionLabels, ListInjectionLabelCounts, ListLabelsByInjectionID, ListLabelIDsByKeyAndInjectionID, ListInjectionsByProjectID | Reads view models `FaultInjectionNoIssues/WithIssues`; AND-of-labels via Group/Having (`injection.go:282-285`); OnConflict on `(fault_injection_id,label_id)` (`injection.go:358`); uses `Scopes(database.Sort)` (`injection.go:255,297`) |
| `label.go` | Label | BatchCreate/Delete/Update/IncreaseLabel(s)/Decrease usage, CreateLabel, DeleteLabel, GetLabelByID, GetLabelByKeyAndValue (variadic statuses), ListLabels, ListLabelsByConditions, ListLabelIDsByConditions, ListLabelsByID, ListLabelsGroupByCategory, SearchLabels, UpdateLabel | `clause.Returning` upsert (`label.go:75`); usage_count increment expression `usage_count + ?`; OR-condition group construction |
| `permission.go` | Permission, RolePermission | BatchUpsert/Create/Delete/Get(ByID/Name)/List etc.; `CheckUserHasPermission` and 5 builder helpers `buildDirect/Global/Team/Project/Container/DatasetRolePermissionQuery` | Subquery composition via `db.Table("(? UNION ALL ?) as base", ...)` for permission resolution (`permission.go:218-241`); raw scope queries against `user_permissions`, `role_permissions`, `user_roles`, `user_teams`, `user_projects`, `user_containers`, `user_datasets` |
| `project.go` | Project, ProjectLabel, UserProject | CreateProject, DeleteProject, GetProjectByID, GetProjectByName, GetProjectUserCount, GetUserProjectRole, ListProjects, ListProjectsByID, BatchGetProjectStatistics, UpdateProject, AddProjectLabels, ClearProjectLabels, RemoveLabelsFromProject, RemoveProjectsFromLabel(s), ListProjectLabels, ListProjectLabelCounts, ListLabelsByProjectID, GetProjectTeamID, ListLabelIDsByKeyAndProjectID | `BatchGetProjectStatistics` (`project.go:122-185`) walks `fault_injections → tasks → traces` and `executions → tasks → traces` to derive per-project counters; uses local `projectOmitFields` (referenced but defined elsewhere — see Surprises) |
| `query_builder.go` | (cross-cutting) | `NewSearchQueryBuilder[F]`, `(qb).ApplySearchReq`, `ExecuteSearch[T,F]`, `resolveMultiValues` | Generic SearchQueryBuilder (see §4); whitelist sort fields; `sanitizeFieldName` allows only `[A-Za-z0-9_.]` |
| `resource.go` | Resource | BatchUpsertResources, GetResourceByID, GetResourceByName, ListResources, ListResourcesByNames, SearchResources | OnConflict on `name` upsert (`resource.go:19`) |
| `role.go` | Role, RolePermission | BatchUpsertRoles, CreateRole, DeleteRole, GetRoleByID, GetRoleByName, GetRolePermissions, ListRoles, ListRolesByIDs, ListSystemRoles, UpdateRole, BatchCreateRolePermissions, BatchDeleteRolePermisssions, RemoveRolesFromPermission, RemovePermissionsFromRole | OnConflict; JOIN `roles → role_permissions → permissions` (`role.go:70+`) |
| `sdk_evaluation.go` | SDKEvaluationSample, SDKDatasetSample | ListSDKEvaluations, GetSDKEvaluationByID, ListSDKExperiments, ListSDKDatasetSamples; `isTableNotExistError` helper | Special handling: returns empty result if table missing (since SDK manages those tables) |
| `system.go` | System | ListSystems, GetSystemByID, GetSystemByName, CreateSystem, UpdateSystem, DeleteSystem (soft), ListEnabledSystems | trivial CRUD |
| `system_metadata.go` | SystemMetadata | GetSystemMetadata, ListSystemMetadata, UpsertSystemMetadata, DeleteSystemMetadata, ListServiceNames | OnConflict-upsert on `(system_name, metadata_type, service_name)` |
| `task.go` | Task (DB) + Redis queues | Redis: SubmitImmediateTask, GetTask, HandleFailedTask, SubmitDelayedTask, ProcessDelayedTasks, HandleCronRescheduleFailure, AcquireConcurrencyLock, InitConcurrencyLock, ReleaseConcurrencyLock, GetTaskQueue, ListDelayedTasks, ListReadyTasks, RemoveFromList, RemoveFromZSet, DeleteTaskIndex, ExpediteDelayedTask. DB: UpdateTaskExecuteTime, BatchDeleteTasks, GetTaskByID, GetTaskWithParentByID, GetParentTaskLevelByID, ListTasks, ListTasksByTimeRange, UpdateTaskState, UpdateTaskStatus, UpsertTask, GetTaskStatistics, GetRecentTaskActivity | Redis Lua script for moving delayed→ready (`task.go:86-98`); `TxPipeline` for ZRem+ZAdd+HSet on rescore (`task.go:274-283`); Preload chain `FaultInjection.{Benchmark,Pedestal}.Container, Execution.{AlgorithmVersion.Container,Datapack,DatasetVersion}` |
| `team.go` | Team, UserTeam | CreateTeam, DeleteTeam, Get(ByID/Name)Team, GetTeamUserCount, GetTeamProjectCount, ListTeams, UpdateTeam, ListProjectsByTeamID, CreateUserTeam, DeleteUserTeam, GetUserTeamRole, ListTeamsByUserID, ListUserTeamsByUserID, ListUsersByTeamID, RemoveUsersFromTeam, RemoveTeamsFromRole, RemoveTeamsFromUser | JOIN `user_teams` to drive ListUsers/ListTeams |
| `token.go` | (Redis only) | AddTokenToBlacklist, AddUserTokensToBlacklist, IsTokenBlacklisted, IsUserBlacklisted, GetBlacklistedTokensCount | SCAN keys `blacklist:token:*` (`token.go:84`) |
| `trace.go` | Trace | GetTraceByID, ListTraces, GetTracesByGroupID, CountTracesByGroupID, ListTraceIDs, UpsertTrace | OnConflict upsert on `id` (`trace.go:113-122`); preload Tasks ordered by level/sequence |
| `user.go` | User, UserRole, UserPermission, UserContainer, UserDataset, UserProject | Create/Delete/Get(ByID/Username/Email)/List/UpdateUser, UpdateUserLoginTime, Create/DeleteUserRole, IsSystemAdmin, RemoveUsersFromRole, RemoveRolesFromUser, GetRoleUserCount, ListUsersByRoleID, ListRolesByUserID, BatchCreateUserPermissions, BatchDeleteUserPermisssions, RemoveUsersFromPermission, RemovePermissionsFromUser, ListPermissionsByUserID, Create/DeleteUserContainer, ListContainersByUserID, ListUserContainersByUserID, RemoveUsersFromContainer, RemoveContainersFromRole/User, Create/DeleteUserDataset, ListDatasetsByUserID, ListUserDatasetsByUserID, RemoveUsersFromDataset, RemoveDatasetsFromRole/User, Create/DeleteUserProject, ListProjectsByUserID, ListUserProjectsByUserID, RemoveUsersFromProject, RemoveProjectsFromRole/User | JOIN-driven projection of containers/datasets/projects per user; `IsSystemAdmin` walks `user_roles → roles` for `super_admin/admin` |

## 4. query_builder / scope / common helpers

### `repository/query_builder.go`
Generic, type-parameterised search engine:
- `SearchQueryBuilder[F ~string]` (`:15`) holds `db`, `query`, `allowedSortFields map[F]string` (whitelist user-facing → DB column).
- `NewSearchQueryBuilder` constructor (`:24`).
- `ApplySearchReq(filters, keyword, sortOptions[F], groupBy[F], modelType)` (`:33`).
- Internal: `applyFilters / applySingleFilter / applyKeywordSearch / applyIncludes / applyIncludeFields / applyExcludeFields / applyPagination / applySorting / getCount / getSearchableFields / getTypeName / sanitizeFieldName`.
- Hard-coded searchable-field map for `User, Role, Permission, Project, Task, Dataset, Container` (`:254-262`).
- `ExecuteSearch[T,F](db, *SearchReq[F], modelType T, allowedSortFields map[F]string) ([]T, int64, error)` (`:297`) — high-level entrypoint.
- `resolveMultiValues(SearchFilter) []string` (`:331`) — accepts JSON arrays or single value.
- 14 filter operators bridge `dto.SearchFilter.Operator` → SQL (Op{Equal,NotEqual,Greater,GreaterEq,Less,LessEq,Like,StartsWith,EndsWith,NotLike,In,NotIn,IsNull,IsNotNull,DateEqual,DateAfter,DateBefore,DateBetween}).
- Used outside this slice in `service/producer/dataset.go:188` and `service/producer/injection.go:285` (each pairs with an `…AllowedFields` map from `consts/search_fields.go`).

### `database/scope.go`
GORM scopes (no DTO/consts deps):
- `KeywordSearch(keyword, fields...)` — builds `field LIKE ? OR …` (`:10`).
- `CursorPaginate(lastID, size)` — `id > ?` cursor pagination (`:26`).
- `Paginate(pageNum, pageSize)` — offset/limit pagination (`:36`).
- `Sort(sort)` — `ORDER BY` with default `id desc` (`:44`).

Within this slice only `database.Sort` is used (`repository/injection.go:255,297` — and even there it's passed an unusual `"dataset_id desc"` against `fault_injection_no_issues` view which has no such column — see Surprises).

### `repository/common.go`
Single constant `commonOmitFields = "active_name"` (`:4`) — used in `repository/{role,permission,resource}.go` to keep GORM from writing the virtual column.

`containerVersionOmitFields = "active_version_key,HelmConfig,EnvVars"` lives in `repository/container.go:15` but is also referenced from `repository/granularity.go:32` — likely a copy-paste mistake (granularity rows have none of these fields).

`projectOmitFields` is referenced in `repository/project.go:189` but is **not defined inside the audited slice**; it must live in a sibling file (cross-slice).

## 5. DTO → entity conversion points

Validation tags (Gin `binding:`) appear in 27 of 35 dto files (313 occurrences in total). Producer naming convention: `service/producer/<name>.go` consumes `dto.<Name>*Req/Resp`.

| dto file | Notable struct(s) | Converters | Validation tags | Producer |
|---|---|---|---|---|
| `algorithm_result.go` | DetectorResultItem, GranularityResultItem, UploadDetector/GranularityResultReq, UploadExecutionResultResp | `(DetectorResultItem).ConvertToDetectorResult(executionID)` (`:50`); `(GranularityResultItem).ConvertToGranularityResult(executionID)` (`:109`) | binding required/oneof | `service/producer/execution.go` |
| `analyzer.go` | AnalyzeTracesReq, ServiceCoverageItem, AttributeCoverageItem, InjectionDiversity, InjectionStats, AnalyzeInjectionsResp | none | binding | analyzer subsystem (none in slice) |
| `audit.go` | ListAuditLogFilters, ListAuditLogReq, AuditLogResp, AuditLogDetailResp | `(ListAuditLogReq).ToFilterOptions()` (`:56`) | binding | `service/producer/audit.go` (cross-slice) |
| `auth.go` | RegisterReq, LoginReq, TokenRefreshReq, ChangePasswordReq, LoginResp, TokenRefreshResp, UserInfo | none | binding required/email/min/max | `service/producer/auth.go` |
| `chaos_system.go` | Create/Update/List/ChaosSystemResp; UpsertSystemMetadataReq, BulkUpsertSystemMetadataReq | none | binding | chaos system handlers |
| `common.go` | PaginationInfo, PaginationReq, SortField + private validators | `(PaginationReq).ToGormParams()`, `.ConvertToPaginationInfo(total)` (`:42,54`) | binding required/oneof; `Validate()` defaults Page=1 / Size=Medium | shared |
| `container.go` | ContainerSpec, CreateContainerReq, CreateContainerVersionReq, CreateHelmConfigReq, CreateParameterConfigReq, BuildOptions, SubmitBuildContainerReq, ContainerResp, etc. (1031 lines, largest) | `ConvertToContainer/ContainerVersion/HelmConfig/ParameterConfig`; `ConvertToSearchRequest` (SearchContainerReq); `BuildOptions.Validate`, `.ValidateRequiredFiles`; `SubmitBuildContainerReq.Validate`, `.ValidateInfoContent` | 58 binding tags | `service/producer/container.go` |
| `dataset.go` | CreateDatasetReq, ListDatasetReq, SearchDatasetReq, UpdateDatasetReq, ManageDatasetLabelReq, ManageDatasetVersionInjectionReq, DatasetResp/Detail, CreateDatasetVersionReq, etc. | `(CreateDatasetReq).ConvertToDataset`; `(SearchDatasetReq).ConvertToSearchReq[consts.DatasetField]`; `(CreateDatasetVersionReq).ConvertToDatasetVersion` | 23 | `service/producer/dataset.go` |
| `debug.go` | DebugGetReq, DebugSetReq | none | binding | debug handler |
| `dynamic_config.go` | List/Update/RollbackConfigReq, ConfigResp/Detail, ConfigHistoryResp, ConfigStatsResp, ConfigUpdateResponse | `(ConfigUpdateResponse).ToMap()` (`:290`) | 17 binding | `service/producer/dynamic_config.go` |
| `evaluation.go` | ListEvaluationReq, EvaluationResp, EvaluateDatapack/DatasetSpec, BatchEvaluate*Req/Resp | none direct (response side only) | 8 | `service/producer/evaluation.go` |
| `execution.go` | ExecutionRef, BatchDeleteExecutionReq, ListExecutionReq, ManageExecutionLabelReq, ExecutionSpec, SubmitExecutionReq, ExecutionResp/Detail | none direct | 12 | `service/producer/execution.go` |
| `group.go` | GroupStreamEvent, GetGroupStreamReq | `(GroupStreamEvent).ToRedisStream()` (`:20`) | 1 | trace SSE handler |
| `injection.go` | InjectionItem, BatchDeleteInjectionReq, CloneInjectionReq, ListInjectionReq, SearchInjectionReq, FriendlyFaultSpec, SubmitInjectionReq, UpdateGroundtruthReq, UploadDatapackReq, view-result Resp types, etc. (1039 lines, second largest) | `(ListInjectionReq).ToFilterOptions`; `(SearchInjectionReq).ConvertToSearchReq[consts.InjectionField]`; `(SubmitDatapackBuildingReq).Validate`; `(BuildingSpec).Validate`; `(UploadDatapackReq).ParseGroundtruths()` returns `[]database.Groundtruth` (`:1024`) | 42 | `service/producer/injection.go` + `service/common/injection.go` |
| `injection_test.go` | (test fixtures) | `mockConverter`, `TestResolveSpecs_FriendlyConverterError` | — | tests only |
| `label.go` | LabelItem, BatchDeleteLabelReq, CreateLabelReq, ListLabelFilters, ListLabelReq, UpdateLabelReq, LabelResp/Detail | `ConvertLabelItemsToConditions([]LabelItem)`; `(CreateLabelReq).ConvertToLabel`; `(ListLabelReq).ToFilterOptions` | 12 | `service/producer/label.go` |
| `log.go` | LogEntry, WSLogMessage | none | — | log streaming handler |
| `metrics.go` | GetMetricsReq, InjectionMetrics, ExecutionMetrics, AlgorithmMetrics, AlgorithmMetricItem | none | 4 | metrics handler |
| `notification.go` | NotificationEvent, GetNotificationStreamReq | none | — | notification SSE |
| `permission.go` | CheckPermissionParams, CreatePermissionReq, ListPermissionReq, SearchPermissionReq, UpdatePermissionReq, PermissionResp/Detail | `(CreatePermissionReq).ConvertToPermission`; `(SearchPermissionReq).ConvertToSearchRequest` | 12 | `service/producer/permission.go` |
| `project.go` | CreateProjectReq, ListProjectReq, SearchProjectReq, UpdateProjectReq, ProjectResp, ProjectStatistics, ProjectDetailResp, ManageProjectLabelReq | `(CreateProjectReq).ConvertToProject`; `(SearchProjectReq).ConvertToSearchRequest` | 7 | `service/producer/project.go` |
| `rate_limiter.go` | RateLimiterHolder, RateLimiterItem, RateLimiterListResp, RateLimiterGCResp | none | — | rate-limiter handler |
| `redis.go` | RdbMsg | none | — | redis stream consumer |
| `request.go` | TimeRangeQuery, TimeFilterOptions | `(TimeRangeQuery).Convert()` returns `*TimeFilterOptions` (`:25`) | 3 | shared |
| `resource.go` | ListResourceReq, ResourceResp, ResourceDetailResp | none | 2 | `service/producer/resource.go` |
| `response.go` | (generic Response wrappers) | none | — | shared |
| `role.go` | CreateRoleReq, ListRoleReq, SearchRoleReq, UpdateRoleReq, AssignRolePermissionReq, RoleResp/Detail | `(CreateRoleReq).ConvertToRole`; `(SearchRoleReq).ConvertToSearchRequest` | 16 | `service/producer/role.go` |
| `sdk_evaluation.go` | ListSDKEvaluationReq, SDKExperimentListResp, ListSDKDatasetSampleReq | none | — | `service/producer/sdk_evaluation.go` |
| `search.go` | SearchFilter, SortOption, TypedSortOption, SearchReq[F], DateRange, NumberRange, AdvancedSearchReq, AlgorithmSearchReq, SearchGroupNode | `(SearchReq[F]).GetOffset/HasFilter/GetFilter/AddFilter/AddInclude/AddSort/SortOptions/GroupByStrings`; `(AdvancedSearchReq).ConvertAdvancedToSearch`; `(AlgorithmSearchReq).ConvertToSearchRequest`; `BuildGroupTree[T,F]` | 11 | shared (paired with query_builder) |
| `system.go` | HealthCheckResp, ServiceInfo, NsMonitorItem, ListNamespaceLockResp, SystemInfo, MonitoringQueryReq, MetricValue, MonitoringMetricsResp, SystemMetricsResp, SystemMetricsHistoryResp | none | 1 | `service/producer/system.go` |
| `task.go` | RetryPolicy, UnifiedTask (large struct), BatchDeleteTaskReq, ListTaskFilters, ListTaskReq, TaskResp/Detail, QueuedTasksResp | `(UnifiedTask).ConvertToTask() *database.Task` (`:49`); `(UnifiedTask).ConvertToTrace(withAlgorithms,leafNum) *database.Trace` (`:71`); `(ListTaskReq).ToFilterOptions` | 8 | `service/common/task.go`, `service/consumer/task.go` |
| `team.go` | CreateTeamReq, ListTeamReq, UpdateTeamReq, TeamResp/Detail, ListTeamMemberReq, AddTeamMemberReq, UpdateTeamMemberRoleReq, TeamMemberResp | `(CreateTeamReq).ConvertToTeam` | 8 | `service/producer/team.go` |
| `trace.go` | TraceStreamEvent, DatapackInfo/Result, ExecutionInfo/Result, InfoPayloadTemplate, TaskScheduledPayload, JobMessage, TraceQuery, GetTraceStreamReq, GetGroupStatsReq, TraceStatsItem, GroupStats, TraceResp/Detail, ListTraceFilters, ListTraceReq | `(TraceStreamEvent).ToRedisStream` (`:24`); `(TraceStreamEvent).ToSSE` (`:41`); `(ListTraceReq).ToFilterOptions` (`:306`) | 7 | trace producer + SSE |
| `user.go` | CreateUserReq, ListUserReq, UserSearchReq, UpdateUserReq, UserResp/Detail/Profile, AssignUserPermissionItem/Req, RemoveUserPermissionReq, UserContainerInfo, UserDatasetInfo, UserProjectInfo | `(UserSearchReq).ConvertToSearchReq`; `(AssignUserPermissionItem).ConvertToUserPermission` | 20 | `service/producer/user.go` |

## 6. consts package

### `consts/consts.go` (565 lines)
- General defaults: `InitialFilename="data.yaml"`, `DetectorKey="algo.detector"`, `DefaultContainerVersion`, `DefaultContainerTag`, `DefaultInvalidID`, `DefaultLabelUsage`, `DefaultTimeUnit`.
- Monitoring: `NamespacesKey="monitor:namespaces"`, `NamespaceKeyPattern="monitor:ns:%s"`.
- Dynamic config etcd prefixes: `ConfigEtcdProducerPrefix`, `…ConsumerPrefix`, `…GlobalPrefix`, `ConfigUpdateResponseChannel="config:updates:response"`.
- `Hybrid chaos.ChaosType = -1` sentinel.
- Eval type strings: `EvalTypeDatapack`, `EvalTypeDataset`.
- Enums (with int kind):
  - `DatapackSource{injection,manual}`; `GroundtruthSource{auto,manual,imported}`.
  - `LabelCategory{System,Config,Container,Dataset,Project,Injection,Execution}`.
  - `AuditLogState{Failed,Success}`, `BuildSourceType{file,github,harbor}`.
  - `ConfigHistoryChangeType{Update,Rollback}`; `ConfigHistoryChangeField{Value,Description,DefaultValue,MinValue,MaxValue,Pattern,Options}`.
  - `ContainerType{Algorithm,Benchmark,Pedestal}`.
  - `ConfigScope{Producer,Consumer,Global}`; `ConfigValueType{String,Bool,Int,Float,StringArray}`.
  - `ParameterType{Fixed,Dynamic}`; `ParameterCategory{EnvVars,HelmValues}`; `ValueDataType{String,Int,Bool,Float,Array,Object}`.
  - `DatapackState{Initial,InjectFailed,InjectSuccess,BuildFailed,BuildSuccess,DetectorFailed,DetectorSuccess}` — has custom `MarshalJSON`.
  - `ExecutionState{Initial,Failed,Success}` — custom Marshal/UnmarshalJSON.
  - `GrantType{Grant,Deny}`.
  - `PageSize{Tiny=5,Small=10,Medium=20,Large=50,XLarge=100}`.
  - `TaskState{Cancelled=-2,Error=-1,Pending=0,Rescheduled=1,Running=2,Completed=3}`.
  - `TraceType{FullPipeline,FaultInjection,DatapackBuild,AlgorithmRun}`; `TraceState{Pending,Running,Completed,Failed}` (MarshalBinary).
  - `TaskType{BuildContainer,RestartPedestal,FaultInjection,RunAlgorithm,BuildDatapack,CollectResult,CronJob}`.
  - `StatusType{Deleted=-1, Disabled=0, Enabled=1}`.
  - `TaskExtra{InjectionAlgorithms="injection_algorithms"}`.
- Detector: `DetectorNoAnomaly="no_anomaly"`.
- Task message templates: `TaskMsgCompleted/Failed`.
- **Payload keys** (string keys put inside Task.Payload JSON): build (`BuildImageRef`, `BuildSourcePath`, `BuildBuildOptions`, `BuildOption{ContextDir,DockerfilePath,Target,BuildArgs,ForceRebuild}`), restart (`RestartPedestal`, `RestartHelmConfig`, `RestartIntarval`, `RestartFaultDuration`, `RestartInjectPayload`), inject (`InjectBenchmark`, `InjectPreDuration`, `InjectNodes`, `InjectGuidedConfigs`, `InjectNamespace`, `InjectPedestal`, `InjectPedestalID`, `InjectLabels`, `InjectSystem`), datapack build (`BuildBenchmark`, `BuildDatapack`, `BuildDatasetVersionID`, `BuildLabels`), execution (`ExecuteAlgorithm`, `ExecuteDatapack`, `ExecuteDatasetVersionID`, `ExecuteEnvVars`, `ExecuteLabels`), collection (`CollectAlgorithm`, `CollectDatapack`, `CollectExecutionID`), evaluation (`EvaluateLabel="app_name"`, `EvaluateLevel="level"`).
- Harbor timeouts (`HarborTimeout=30s`).
- Redis: `InjectionAlgorithmsKey="injection:algorithms"`. Streams: `StreamTraceLogKey="trace:%s:log"`, `StreamGroupLogKey="group:%s:log"`, `NotificationStreamKey="notifications:global"`. Stream field names: `RdbEventTaskID/TaskType/Status/FileName/Line/Name/Payload/Fn/TraceID/TraceState/TraceLastEvent`.
- Token-bucket rate limiters: `TokenWaitTimeout=10`; per-service: `RestartPedestalTokenBucket="token_bucket:restart_service"`, `BuildContainerTokenBucket="token_bucket:build_container"`, `AlgoExecutionTokenBucket="token_bucket:algo_execution"`; matching `MaxConcurrent*` defaults (2,3,5) and dynamic-config keys (`MaxTokensKey…`).
- **EventType** (string enum, ~30 values) used for trace SSE: `restart.pedestal.{started,completed,failed}`, `fault.injection.{started,completed,failed}`, `algorithm.run.{started,succeed,failed}`, `algorithm.{result.collection,no_result_data}`, `datapack.build.{started,succeed,failed}`, `datapack.{result.collection,no_anomaly,no_detector_data}`, `image.build.{started,succeed,failed}`, `task.{started,state.update,retry.status,scheduled}`, `no.{namespace,token}.available`, `acquire.lock`, `release.lock`, `k8s.job.{succeed,failed}`. Has `String()` and `MarshalBinary()`.
- Tracing carrier keys: `TaskCarrier`, `TraceCarrier`, `GroupCarrier`.
- **K8s annotation/label keys**: `CRDAnnotationBenchmark`, `JobAnnotationAlgorithm`, `JobAnnotationDatapack`, `K8sLabelAppID="rcabench_app_id"`, `CRDLabelBatchID`, `CRDLabelIsHybrid`, `JobLabelName="job-name"`, `JobLabelTaskID/TraceID/GroupID/ProjectID/UserID/TaskType`, `JobLabelDatapack/DatasetID/ExecutionID/Timestamp`.
- Volume mount enum: `VolumeMountKubeConfig|Dataset|ExperimentStorage`.
- SSE event names: `EventEnd|EventUpdate`. WS log types: `WSLogTypeHistory|Realtime|End|Error`. Log levels: `LogLevelError|Warn|Info|Debug`.
- File constants: `DownloadFilename="package"`, `DetectorConclusionFile="conclusion.csv"`, `ExecutionResultFile="result.csv"`.
- Granularity placeholder keys `DurationNodeKey="0"`, `SystemNodeKey="1"`.
- Span attribute keys: `TaskIDKey/TaskTypeKey/TaskStateKey`.
- URL path constants: `URLPath{ID,UserID,RoleID,PermissionID,ConfigID,ContainerID,VersionID,DatasetID,ProjectID,TeamID,TaskID,DatapackID,ExecutionID,AlgorithmID,InjectionID,TraceID,GroupID,LabelID,ResourceID,Name}`.
- Two package vars: `AppID string`, `InitialTime *time.Time`.

### `consts/collection.go`
Bidirectional mapping tables (enum int → name + `Get…Name` and `Get…ByName` helpers): `auditLogStateMap`, `containerTypeMap`, `configHistoryChangeTypeMap`, `ConfigScopeMap` (exported), `dynamicConfigTypeMap`, `datapackStateMap`, `executeStateMap`, `grantTypeMap`, `labelCategoryMap`, `parameterTypeMap`, `parameterCategoryMap`, `valueDataTypeMap`, `resourceDisplayNameMap`, `resouceTypeMap` (sic), `resourceCategoryMap`, `statusTypeMap`, `taskStateMap`, `taskTypeMap`, `traceTypeMap`, `traceStateMap`, plus `SystemRoleDisplayNames map[RoleName]string`. Exposed accessors: `GetAuditLogStateName/ContainerTypeName/ConfigHistoryChangeTypeName/ConfigScopeName/DatapackStateName/DatapackStateByName/DynamicConfigTypeName/ExecutionStateName/ExecutionStateByName/GrantTypeName/LabelCategoryName/ParameterTypeName/ParameterCategoryName/ValueDataTypeName/ResourceDisplayName/ResourceTypeName/ResourceCategoryName/StatusTypeName/TaskStateName/TaskStateByName/TaskTypeName/TaskTypeByName/TraceTypeName/TraceStateName`.

### `consts/errors.go`
Six sentinel errors used everywhere via `errors.Is`: `ErrAuthenticationFailed`, `ErrPermissionDenied`, `ErrNotFound`, `ErrAlreadyExists`, `ErrBadRequest`, `ErrInternal`.

### `consts/export.go`
String-typed exports for SDK / external use: `DatapackStateString` (`initial,inject_failed,inject_success,build_failed,build_success,detector_failed,detector_success`); `ExecutionStateString` (`Initial,Failed,Success`).

### `consts/search_fields.go`
Whitelists for the generic SearchQueryBuilder:
- `InjectionField` enum + `InjectionAllowedFields map[InjectionField]string` for `name, fault_type, category, state, start_time, end_time, created_at, updated_at`.
- `DatasetField` enum + `DatasetAllowedFields` for `name, is_public, created_at, updated_at`.

### `consts/system.go`
Permission/RBAC truth source:
- Action enum (`ActionName`): 23 values — Create/Read/Update/Delete, Execute/Stop/Restart/Activate/Suspend, Upload/Download/Import/Export, Assign/Grant/Revoke, Configure/Manage, Share/Clone, Monitor/Analyze/Audit.
- Resource enum (`ResourceName`): 17 values — system, audit, configuration, container, container_version, dataset, dataset_version, project, team, label, user, role, permission, task, trace, injection, execution.
- Scope filters: `ProjectScopedResources` (injection, execution, task, trace) and `TeamScopedResources` (container, container_version, dataset, dataset_version, project, label) with `IsProjectScoped` / `IsTeamScoped` helpers.
- `ResourceType{System,Table}`, `ResourceCategory{Chaos,Asset,Platform,System}`, `ResourceScope{Own,Project,Team,All}`.
- `RoleName` enum (16 roles): super_admin, admin, user, container_{admin/developer/viewer}, dataset_{admin/developer/viewer}, project_{admin/algo_developer/data_developer/viewer}, team_{admin/member/viewer}.
- `PermissionRule{Resource, Action, Scope}` + `ParsePermissionRule(string)` for `resource:action:scope` strings.
- ~120 predefined `PermXxx` permission rule constants and `SystemRolePermissions map[RoleName][]PermissionRule` defining full ACL matrix.

### `consts/validation.go`
Dictionary sets used by DTO `Validate()`/binding code: `ValidActions`, `ValidResourceNames`, `ValidResourceScopes`, `ValidAuditLogStates`, `ValidContainerTypes`, `ValidConfigHistoryChanteTypes` (sic), `ValidConfigScopes` (note: omits `Global`), `ValidDatapackStates` (note: omits Detector* — see Surprises), `ValidDynamicConfigTypes`, `ValidExecutionStates`, `ValidGrantTypes`, `ValidLabelCategories`, `ValidPageSizes`, `ValidParameterTypes`, `ValidParameterCategories`, `ValidResourceTypes`, `ValidResourceCategories`, `ValidStatuses`, `ValidVolumeMountNames`, `ValidTaskStates`, `ValidTaskTypes` (omits CronJob), `ValidTraceStates`, `ValidTraceTypes`. Plus `ValidTaskEvents map[TaskType][]EventType` mapping each TaskType to its expected EventType set.

## 7. Views / read models

`database/view.go`:
- `FaultInjectionNoIssues` / `FaultInjectionWithIssues` — read-only structs mapping to MySQL views of the same names. Composed of (`fi.id, name, fault_type, category, engine_config, label_key, label_value, created_at`) plus, for the “WithIssues” variant, detector metrics (`issues, abnormal/normal_avg_duration/succ_rate/p99`).
- `addDetectorJoins(query)` (`view.go:50`) — appends a window-function-ranked subquery to keep only the latest Execution per `(algorithm_id=1, datapack_id)` and joins `detector_results`. **Hard-coded algorithm id=1** in the JOIN.
- `createDetectorViews()` (`view.go:69`) — drops + recreates both views via GORM's `Migrator().CreateView` on each boot (called from `database.go:117` after AutoMigrate). Both views additionally LEFT JOIN `fault_injection_labels` and `labels` and group by `(fi.*, l.label_key, l.label_value)`.
- The `FaultInjectionWithIssues` query joins through `tasks` even though the `Select` doesn't reference any column of it (likely vestigial).

These views are read by `repository/injection.go:254-335` (`ListInjectionsNoIssues` / `ListInjectionsWithIssues`) and surfaced via `dto.InjectionNoIssuesResp` / `dto.InjectionWithIssuesResp`.

GORM scopes referenced by repositories (only `database.Sort` is actually used inside this slice; the rest are unused — see Surprises).

## 8. Detector / granularity / sdk_evaluation

- **Detector (`repository/detector.go` + `database/entity.go:608` + `view.go`)** — Per-Execution per-SpanName statistical comparison between an "abnormal" window (during fault injection) and a "normal" baseline window. Captures average duration, success rate, P90/P95/P99 for both windows, plus a free-form `Issues` string (used by the views to bucket "no issues" vs "with issues"). The repository layer is intentionally thin (one list, one bulk save); the heavy lifting is the SQL views that pick the latest per-(algo,datapack) execution and pivot detector rows into a queryable view of fault-injection outcomes.

- **Granularity (`repository/granularity.go` + `database/entity.go:628`)** — Models per-Execution algorithm output: a ranked list of "located" entities at a given `Level` (`service`, `pod`, `span`, `metric` per `consts.DurationNodeKey/SystemNodeKey` and `EvaluateLevel="level"`). Each row carries a `Result` (comma-separated entity list), `Rank` (top-1, top-2…), and optional `Confidence`. Repository exposes only list-by-execution and bulk save; rows are created one-by-one with `Omit(containerVersionOmitFields)` and translate `gorm.ErrDuplicatedKey` into `consts.ErrAlreadyExists`.

- **SDK Evaluation (`repository/sdk_evaluation.go` + `database/sdk_entities.go`)** — Read-only bridge to two tables (`data`, `evaluation_data`) that the **Python SDK** owns and writes to. The Go side never migrates them (`AutoMigrate` excludes both) and tolerates missing tables: `isTableNotExistError` (`sdk_evaluation.go:14`) catches the MySQL "doesn't exist" error and degrades to empty results. Surfaces three list endpoints (paginated samples, evaluations by `(exp_id, stage)`, distinct experiments) plus get-by-id. The richer `SDKEvaluationSample` schema captures end-to-end agent-evaluation runs including raw question, model name, judged response, trajectories JSON, etc.

## 9. Data layer outbound edges (non-stdlib, non-GORM)

Imports observed in `repository/`:
- `aegis/client` — Redis client provider (`repository/{task,token}.go`).
- `aegis/consts` — every repo file uses it.
- `aegis/database` — every repo file (and DTOs in some).
- `aegis/dto` — `repository/{audit,injection,label,permission,project,query_builder,task,trace}.go`.
- `gorm.io/gorm` — universal.
- `gorm.io/gorm/clause` — for `OnConflict`, `Returning` (`container, dataset, execution, injection, label, permission, resource, role, system_metadata, task, trace`).
- `github.com/redis/go-redis/v9` — `repository/task.go` only (Lua script + ZAdd/HSet primitives).
- `github.com/sirupsen/logrus` — `repository/task.go`.
- *Indirect* (via `database/`): `gorm.io/driver/mysql`, `gorm.io/plugin/opentelemetry/tracing` (`database/database.go`); `github.com/OperationsPAI/chaos-experiment/handler` (re-exposed as `chaos`) for `ChaosType`, `SystemType`, `Groundtruth` types; `aegis/utils` and `aegis/config`.

DTOs additionally import `github.com/OperationsPAI/chaos-experiment/handler` (chaos types in injection requests) and reuse `aegis/database` for converter return types.

## 10. Surprises / dead code / orphans

1. **`database/scope.go` is almost dead.** `KeywordSearch`, `CursorPaginate`, `Paginate` have **zero** call sites in the audited slice. Only `Sort` is used, twice, both with the suspicious sort key `"dataset_id desc"` against the `fault_injection_no_issues/with_issues` views which expose `datapack_id`, not `dataset_id` (`repository/injection.go:255,297`). Either dead column reference or a renaming bug.

2. **`ListInjectionsWithIssues` queries the wrong model** — the function takes "WithIssues" results into `[]FaultInjectionWithIssues` but builds the query against `db.Model(&database.FaultInjectionNoIssues{})` (`repository/injection.go:297`). The actual `FROM` table is the views' shared model name, so this likely "works" only by accident of identical column shape. Bug.

3. **`containerVersionOmitFields` reused for granularity rows** (`repository/granularity.go:32`). The omit list `"active_version_key,HelmConfig,EnvVars"` is meaningless on `GranularityResult` (no such fields) — copy-paste artefact.

4. **`projectOmitFields` is referenced (`repository/project.go:189`) but not defined inside the audited slice.** Either a hidden sibling file in the package or a dangling reference.

5. **Hard-coded algorithm id `1`** in detector views (`database/view.go:65`). Means the `fault_injection_*_issues` views only surface results from container_version where `containers.id == 1`. Magic number with no comment.

6. **`ConfigScope.Global` is defined (`consts.go:136`) and used in `ConfigScopeMap`, but `ValidConfigScopes` (`validation.go:85`) only allows Producer/Consumer.** Validation will reject Global values.

7. **`ValidDatapackStates` omits `DatapackDetectorFailed/Success`** (`validation.go:90-96`) even though those are emitted by the consumer pipeline and observed in the entity definition. Validation will reject the very states the system writes.

8. **`ValidTaskTypes` omits `TaskTypeCronJob`** (`validation.go:203-210`) although it exists in `TaskType` and `taskTypeMap`. Cron tasks will fail validation.

9. **`Trace.LeafNum` and `Task.Sequence` / `Task.Level`** are written by `dto.UnifiedTask.ConvertToTrace/Task` but never read by any function in `repository/` (only `GetParentTaskLevelByID` reads `Level`). Could indicate read paths live elsewhere, or the columns are write-only metadata for downstream tools.

10. **No repository for `Resource.Parent` traversal, `UserPermission` listing by direct grant, `ConfigLabel` join table, or `Evaluation` listing by project.** `BatchUpsertResources` writes a flat list; the parent FK column is populated but no repo function ever reads it. Same for `ConfigLabel`: declared on `DynamicConfig` and migrated, but no repo touches it.

Bonus observations:
- `Project` declares two implicit GORM many2many relations (`project_containers`, `project_datasets`) but neither side has explicit join-table structs and **no repository function references either** — cross-slice service code may handle them.
- `repository/injection.go:97` `ListExistingEngineConfigs` operates on `engine_config` JSON-as-text — string compare on a `longtext` column without an index could be slow at scale.
- `Permission.CheckUserHasPermission` builds nested `(? UNION ALL ?)` subqueries via `db.Table` with placeholder substitution; correctness depends on GORM placing the inner `Statement.SQL` correctly into the outer `Table` parameters. Worth a regression test.
