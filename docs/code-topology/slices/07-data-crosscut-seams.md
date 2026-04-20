# Slice 07 — Data layer, routing, cross-cutting, cross-repo seams

> Historical note: parts of this document were written before the phase-2 gRPC collapse. Cross-check process-level claims against `docs/code-topology/README.md`, `docs/code-topology/slices/01-app-wiring.md`, and `docs/code-topology/slices/06-grpc-interfaces.md` first.

Scope: `model/`, `router/`, `middleware/`, `dto/`, `consts/`, `utils/`, `tracing/`, `httpx/`, `searchx/`; frontend `/api/v2/*` grep; rcabench-platform datapack files; chaos-experiment import surface.

---

## 1. Entity inventory (model/)

All entities in a single file `model/entity.go` (917 lines). Helpers in `entity_helper.go` (Groundtruth JSON scanner/valuer). Views live in `model/view.go`. No `gorm.DeletedAt` anywhere — soft-delete is uniformly via `Status consts.StatusType` (-1 deleted, 0 disabled, 1 enabled) and MySQL **generated virtual columns** (`ActiveName`, `ActiveKeyValue`, etc.) that null out when `status < 0` for unique-index-with-tombstone semantics.

| Struct | Table (default plural) | Key fields / indices | FK | Notes |
|---|---|---|---|---|
| `DynamicConfig` (entity.go:19) | `dynamic_configs` | `config_key+category` uniq; `Scope ConfigScope` | `UpdatedBy→User` | M2M `Labels` via `config_labels` |
| `ConfigHistory` (entity.go:49) | `config_histories` | `ConfigID` idx | `Config→DynamicConfig`, `Operator→User`, `RolledBackFrom→self` | |
| `Container` (entity.go:75) | `containers` | `ActiveName` uniq; `idx_container_status_type` | — | M2M `Labels`; hook BeforeCreate validates pedestal name against `chaos.SystemType` |
| `ContainerVersion` (entity.go:103) | `container_versions` | semver `NameMajor/Minor/Patch`; `idx_active_version_unique` on `ActiveVersionKey` | `Container`, `User` | 1-1 `HelmConfig`; M2M `EnvVars` via `container_version_env_vars`. `ImageRef` computed |
| `HelmConfig` (entity.go:175) | `helm_configs` | `ContainerVersionID` uniq | `ContainerVersion (CASCADE)` | M2M `DynamicValues` via `helm_config_values` |
| `ParameterConfig` (entity.go:212) | `parameter_configs` | `idx_unique_config(key,type,category)` | — | |
| `ContainerVersionEnvVar` (entity.go:251) | `container_version_env_vars` | composite PK | | M2M join |
| `HelmConfigValue` (entity.go:262) | `helm_config_values` | composite PK | | M2M join |
| `Dataset` (entity.go:272) | `datasets` | `ActiveName` uniq; `Status` idx | — | M2M `Labels`, `Versions` |
| `DatasetVersion` (entity.go:290) | `dataset_versions` | semver; `ActiveVersionKey` uniq | `Dataset`, `User` | M2M `Datapacks` via `dataset_version_injections` |
| `Project` (entity.go:329) | `projects` | `ActiveName` uniq; `TeamID` idx | `Team` | M2M `Containers`, `Datasets`, `Labels` |
| `Label` (entity.go:351) | `labels` | `ActiveKeyValue` uniq; `idx_label_key_category` | — | |
| `Team` (entity.go:369) | `teams` | `ActiveName` uniq | — | 1-many `Projects` |
| `User` (entity.go:386) | `users` | `ActiveUsername` uniq; unique `username`, `email` | — | BeforeCreate hashes password via `utils.HashPassword` |
| `APIKey` (entity.go:415) | `api_keys` | `ActiveKeyID` uniq; `idx_api_key_owner_status` | `User` | `Scopes []string` json; `KeySecretCiphertext` stores AES-GCM |
| `Role` (entity.go:437) | `roles` | `ActiveName` uniq | — | |
| `Permission` (entity.go:452) | `permissions` | `idx_perm(action,scope,resource_id)`; `ActiveName` uniq | `Resource` | |
| `Resource` (entity.go:473) | `resources` | `Name` uniq | `Parent→self` | |
| `AuditLog` (entity.go:489) | `audit_logs` | `idx_audit_action_state`, `idx_audit_user_time` | `User`, `Resource` | |
| `Trace` (entity.go:516) | `traces` | `idx_trace_type_state`, `idx_trace_project_state` | `Project` | 1-many `Tasks` |
| `Task` (entity.go:540) | `tasks` | `idx_task_type_status`, `idx_task_project_state`; PK string(64) | `Trace (CASCADE)`, self `ParentTask` | 1-1 `FaultInjection`, `Execution` |
| `FaultInjection` (entity.go:571) | `fault_injections` | `idx_fault_type_state`, `idx_fault_bench_ped` | `Benchmark→ContainerVersion (SET NULL)`, `Pedestal→ContainerVersion (SET NULL)`, `Task (CASCADE)` | `Groundtruths []Groundtruth` json; check `start_time < end_time` |
| `Execution` (entity.go:603) | `executions` | `idx_exec_algo_datapack` | `Task (CASCADE)`, `AlgorithmVersion→ContainerVersion (RESTRICT)`, `Datapack→FaultInjection (RESTRICT)`, `DatasetVersion (SET NULL)` | 1-many `DetectorResult`, `GranularityResult`; M2M `Labels` via `execution_injection_labels` |
| `DetectorResult` (entity.go:629) | `detector_results` | `ExecutionID` | `Execution (CASCADE)` | |
| `GranularityResult` (entity.go:649) | `granularity_results` | `ExecutionID` idx | `Execution (CASCADE)` | |
| `System` (entity.go:666) | `systems` | `Name` uniq | — | chaos system registration |
| `SystemMetadata` (entity.go:681) | `system_metadata` | `idx_system_meta` composite | — | |
| `ContainerLabel` (entity.go:696) | `container_labels` | composite PK | | M2M join |
| `DatasetLabel` (entity.go:707) | `dataset_labels` | composite PK | | M2M join |
| `ProjectLabel` (entity.go:718) | `project_labels` | composite PK | | M2M join |
| `DatasetVersionInjection` (entity.go:729) | `dataset_version_injections` | composite PK | | M2M join |
| `FaultInjectionLabel` (entity.go:740) | `fault_injection_labels` | composite PK | FaultInjection CASCADE | |
| `ExecutionInjectionLabel` (entity.go:750) | `execution_injection_labels` | composite PK | Execution CASCADE | |
| `ConfigLabel` (entity.go:761) | `config_labels` | composite PK | | |
| `UserContainer` (entity.go:772) | `user_containers` | `ActiveUserContainer` uniq | `User`, `Container`, `Role` | |
| `UserDataset` (entity.go:790) | `user_datasets` | `ActiveUserDataset` uniq | `User`, `Dataset`, `Role` | |
| `UserProject` (entity.go:809) | `user_projects` | `ActiveUserProject` uniq | `User`, `Project`, `Role` | holds `WorkspaceConfig` JSON |
| `UserTeam` (entity.go:830) | `user_teams` | `ActiveUserTeam` uniq | `User`, `Team`, `Role` | |
| `UserRole` (entity.go:849) | `user_roles` | `idx_user_role_unique(user_id,role_id)` | `User`, `Role` | global roles |
| `RolePermission` (entity.go:863) | `role_permissions` | `idx_role_permission_unique` | `Role`, `Permission` | |
| `Evaluation` (entity.go:877) | `evaluations` | `ProjectID` idx, `Status` idx | — (stringly by name/version) | new entity |
| `UserPermission` (entity.go:897) | `user_permissions` | 3 composite unique idx on (user,perm,container/dataset/project) | `User`, `Permission`, `Container`, `Dataset`, `Project` | per-user direct grants with `GrantType` + `ExpiresAt` |

**Views (model/view.go)**: `FaultInjectionNoIssues`, `FaultInjectionWithIssues` — both materialized as MySQL views by `infra/db/migration.go:78-122` via `db.Migrator().CreateView(...)`. `TableName()` methods map them.

**Auto-migrate set** (`infra/db/migration.go:11-52`): all 39 entities above. Views are dropped + recreated on every boot (`migration.go:79-80`). SDK creates its own tables outside auto-migrate (per `module/sdk/models.go:6`).

---

## 2. Entity relationship graph

```
User ─┬─< UserRole >── Role ─< RolePermission >── Permission ── Resource ──(self.Parent)
      ├─< UserTeam >── Team ─< Project (TeamID?)
      ├─< UserProject >── Project
      ├─< UserContainer >── Container
      ├─< UserDataset >── Dataset
      ├─< UserPermission >── Permission  (container|dataset|project optional)
      ├─< APIKey
      ├─< AuditLog >── Resource
      └─< ConfigHistory.Operator
          DynamicConfig ─< ConfigHistory (self.RolledBackFrom?)
          DynamicConfig ─<M2M>─ Label   via config_labels

Team ─< Project
Project ─<M2M>─ Container  via project_containers
Project ─<M2M>─ Dataset    via project_datasets
Project ─<M2M>─ Label      via project_labels
Project ─< Trace

Container ─< ContainerVersion ─(1-1)─ HelmConfig
ContainerVersion ─<M2M: env_vars>─ ParameterConfig
HelmConfig       ─<M2M: helm_values>─ ParameterConfig
Container ─<M2M>─ Label    via container_labels

Dataset ─< DatasetVersion
DatasetVersion ─<M2M>─ FaultInjection  via dataset_version_injections (aka Datapacks)
Dataset ─<M2M>─ Label    via dataset_labels

Trace ─< Task ─(self.ParentTask)  CASCADE
Task  ─(1-1)─ FaultInjection       CASCADE
Task  ─(1-1)─ Execution            CASCADE

FaultInjection ─ Benchmark → ContainerVersion  (SET NULL)
FaultInjection ─ Pedestal  → ContainerVersion  (SET NULL)
FaultInjection ─<M2M>─ Label  via fault_injection_labels   CASCADE

Execution ─ AlgorithmVersion → ContainerVersion  (RESTRICT)
Execution ─ Datapack         → FaultInjection    (RESTRICT)
Execution ─ DatasetVersion   → DatasetVersion    (SET NULL)
Execution ─< DetectorResult   CASCADE
Execution ─< GranularityResult CASCADE
Execution ─<M2M>─ Label  via execution_injection_labels  CASCADE

System, SystemMetadata — standalone (linked only by string SystemName)
Evaluation — standalone (links to algorithm/dataset by NAME+VERSION strings, plus optional ProjectID)
```

---

## 3. router/ audience split

Entry `router/router.go:13 New(handlers, middlewareService) *gin.Engine`. Builds root engine, global middleware chain (router.go:25-32):
1. `middleware.InjectService(middlewareService)` — inject fx-provided Service into ctx
2. `middleware.RequestID()` — X-Request-Id
3. `middleware.GroupID()` — uuid on POSTs, X-Group-ID header
4. `middleware.SSEPath()` — sets SSE headers for `^/stream(/.*)?$`
5. `cors.New(config)` — permissive (AllowAllOrigins=true, AllowCredentials=true — see Surprises)
6. `middleware.TracerMiddleware()` — OTEL span per request

Single `v2 := router.Group("/api/v2")` is then split:

| File | Audience | Group middleware | Sample routes |
|---|---|---|---|
| `public.go:9` SetupPublicV2Routes | Public / unauthenticated entry | `/auth` open; `/auth/logout|change-password|profile` adds `JWTAuth + RequireHumanUserAuth` | POST `/auth/login`, POST `/auth/register`, POST `/auth/refresh`, POST `/auth/logout`, POST `/auth/change-password`, GET `/auth/profile` |
| `sdk.go:9` SetupSDKV2Routes | Machine / API-key / service token | `JWTAuth` + `RequireAPIKeyScopesAny("sdk:...")` on SDK groups; `RequireServiceTokenAuth` on runtime upload | POST `/auth/api-key/token`; GET `/sdk/evaluations`, `/sdk/evaluations/experiments`, `/sdk/evaluations/:id`; GET `/sdk/datasets`; GET `/datasets/:dataset_id/versions/:version_id/download`; POST `/projects/:id/injections/inject|build`; GET `/projects/:id/injections`, `/projects/:id/injections/analysis/(no|with)-issues`; POST `/projects/:id/executions/execute`; POST `/evaluations/datapacks|datasets`; GET `/evaluations`, `/evaluations/:id`; GET `/executions/:id`, PATCH `/executions/:id/labels`; POST `/executions/:exec_id/detector_results|granularity_results` (**service-token only**); `/injections/*` (metadata, systems, translate, :id, clone, download, files, files/download, files/query, labels); GET `/metrics/{algorithms,executions,injections}` |
| `admin.go:10` SetupAdminV2Routes | Admin / IAM / system | `JWTAuth` + `RequireX*` permission gates per subgroup | `/users/*` (CRUD + role/project/permission/container/dataset assignment); `/roles/*` (CRUD + role perms + list users); `/permissions/*` (read + roles); `/resources/*` (read + perms); `/systems/*` (chaos system CRUD + metadata); `/system/{metrics,audit,configs,health,monitor}` |
| `portal.go:9` SetupPortalV2Routes | Human UI / portal users | `JWTAuth` + per-resource `RequireXRead/Create/...` gates | `/containers/*` (+ `/build`, `/container-versions/:id/image` flat helper); `/datasets/*` (+ `/search`); `/projects/*` (+ injections/search); `/teams/*` (members/projects); `/labels/*`; `/evaluations/:id` DELETE; `/executions/labels`, `/executions/batch-delete`; `/injections/upload`, `/injections/:id/groundtruth`, batch labels/delete; `/notifications/stream` SSE; `/tasks/*` (+ `/:task_id/logs/ws` WebSocket + expedite); `/pedestal/helm/*`; `/rate-limiters/*`; `/groups/:group_id/{stats,stream}` SSE; `/traces/*` (+ stream SSE); `/api-keys/*` — requires `RequireHumanUserAuth` to block API-key bearer tokens from creating more API keys |

### router/handlers.go
Just a `Handlers` aggregate (router/handlers.go:28-51) and `NewHandlers(...)` fx constructor (line 53-101). Imports exactly 22 module handlers from `aegis/module/*`. Each route file pulls them by `handlers.Injection.*`, `handlers.Auth.*`, etc.

### router/module.go + router.go
`router/module.go:5` provides only `fx.Provide(NewHandlers)`. The engine itself is constructed in `interface/http/module.go:10-17` (`httpapi.Module`), which combines `middleware.NewService` + `router.New` + `NewServer` + lifecycle hook. `app/http_modules.go:61` composes `router.Module` into the producer fx graph.

---

## 4. Middleware chain (per file)

### `middleware/auth.go`
- `JWTAuth()` (auth.go:18-66) — **dual-mode**: tries user token (`VerifyToken`) then service token (`VerifyServiceToken`). Sets ctx keys: `user_id`, `username`, `email`, `is_active`, `is_admin`, `user_roles`, `auth_type`, `api_key_id`, `api_key_scopes`, `token_type`, plus service-token-specific `task_id`, `is_service_token`.
- `OptionalJWTAuth()` (auth.go:72-124) — same but no-abort on missing/invalid.
- Helpers: `GetCurrentUserID`, `GetCurrentUsername`, `GetCurrentUserEmail`, `IsCurrentUserActive`, `IsServiceToken`, `GetTokenType`, `IsCurrentUserAdmin`, `GetCurrentUserRoles`, `GetCurrentAPIKeyScopes` (auth.go:219-227 — **new**), `GetAuthType` (new, reads `auth_type` claim), `GetServiceTaskID`.
- Gate helpers: `RequireAuth` (inline), `RequireUserAuth` (rejects service), `RequireServiceTokenAuth()` (auth.go:288-301), `RequireActiveUser()`.
- **New vs pre-refactor**: `api_key_id`, `api_key_scopes`, `auth_type` claim support; `RequireServiceTokenAuth` middleware form.

### `middleware/api_key_scope.go` (NEW)
- `apiKeyScopeMatchesTarget(scope, target)` (api_key_scope.go:12-40): dot-colon glob. `scope="*"` → match all; scope parts short-circuit match against `:`-split target, `*` wildcard per segment, extra target segments auto-wildcard.
- `apiKeyScopesAllowAnyTarget(scopes, targets)` (line 42-54).
- `RequireHumanUserAuth()` (line 58-71) — rejects `auth_type==api_key` and service tokens. Used on `/api-keys/*` + `/auth/logout|change-password|profile`.
- `RequireAPIKeyScopesAny(targets...)` (line 75-97) — **applies only when `auth_type=="api_key"`**; user JWTs bypass. Typical usage: `RequireAPIKeyScopesAny("sdk:*", "sdk:evaluations:*", "sdk:evaluations:read")` (sdk.go:15, portal.go:137).

### `middleware/audit.go`
- `AuditMiddleware()` (audit.go:22) — captures request body + response body via `bodyLogWriter` (audit.go:113). Skips GETs (`shouldAudit` line 124) and paths: `/system/health`, `/system/monitor/metrics`, `/system/monitor/namespace-locks`.
- `isSensitivePath` (line 197-211): skips body capture for `/auth/login`, `/auth/register`, `/users/password`, `/users/change-password`.
- `sanitizeRequestData` (line 214-221): redacts `password`, `token`, `secret`, `api_key`, `apiKey` fields (`***REDACTED***`).
- Resource inference: `determineResource` (audit.go:161-194) — strips `/api/v2/`, singularizes, specializes `container→container_version` / `dataset→dataset_version` when 5+ path segments.
- NOTE: audit middleware **is not registered in `router.New`** — the global chain at router.go:25 omits `AuditMiddleware()`. Declared but not wired. (See Surprises.)

### `middleware/permission.go`
- `permissionContext` struct (permission.go:16-28) now carries `authType` + `apiKeyScopes`. `scopeAllowsPermission` (line 110-123) + `apiKeyScopeMatchesPermission` (line 134) — permission checks are gated **twice** for API keys: first the scope glob must allow the rule, then the DB check.
- Pre-built vars (permission.go:447-566): all `RequireXRead/Create/Update/Delete/Manage` + team/project access gates + CRUD for every resource (User/Role/Permission/Team/Project/Container/ContainerVersion/Dataset/DatasetVersion/Label/Task/Trace). `RequireSystemAdmin()` checks `ctx.isAdmin` from JWT only.
- Missing: no `RequireEvaluationXxx`, `RequireExecutionXxx` pre-built vars — those use direct `RequirePermission(consts.PermExecutionExecuteProject)` etc. (sdk.go:66).

### `middleware/ratelimit.go`
In-memory per-process `RateLimiter{requests map[string][]time.Time}` (ratelimit.go:14). 3 global buckets: `generalLimiter` (1000/min), `authLimiter` (100/min), `strictLimiter` (20/min). `StartCleanupRoutine()` called from router.New. **Unchanged vs pre-refactor**. Public vars: `GeneralRateLimit` (IP), `AuthRateLimit`, `StrictRateLimit` (user), `UserRateLimit`. `CustomRateLimit(config)` builder. **Not currently mounted anywhere in the v2 routers** — declared but unused. (Module-level `ratelimiter` at `/rate-limiters` uses its own Redis-backed system, per portal.go:177.)

### `middleware/deps.go` (new wiring)
- Defines service interfaces: `TokenVerifier`, `permissionChecker`, `auditLogger` (deps.go:18-35), aggregated into `Service` (line 37-41).
- `NewService(db, verifier) Service` → `dbBackedMiddlewareService` (line 45-47) consumed by fx at `interface/http/module.go:12`.
- `InjectService(service) gin.HandlerFunc` (line 49-58) stores into ctx key `"middleware.service"`.
- Implements all permission checks (`CheckUserPermission` at line 98, with `direct + global_role + team/project/container/dataset` union queries at line 242-264) and audit logging (`LogUserAction`, `LogFailedAction`).
- `noopMiddlewareService` (line 378-411) returned when no service in ctx.

### `middleware/middleware.go`
- `TracerMiddleware()` (line 60-80) — OTEL tracer `rcabench/group`, span name = `METHOD PATH`, span context stored at `SpanContextKey`.
- `SSEPath()` (line 20-33) — regex `^/stream(/.*)?$` (NB: does not match `/foo/stream` so SSE endpoints under `/traces/:id/stream`, `/groups/:id/stream` are NOT auto-headered here).
- `GroupID()` (line 35-45) — POST-only uuid + X-Group-ID header.
- `RequestID()` (line 47-58) — uses `httpx.RequestIDHeader` + `httpx.WithRequestID` context value.

---

## 5. dto/ — still alive

Heavily alive: 146 files across the repo import `"aegis/dto"`. Every module's `api_types.go` imports it for generic response wrappers (`dto.GenericResponse`, `dto.SearchReq`, etc.). Per-file exports:

| File | Exported types / funcs |
|---|---|
| `common.go` | `PaginationInfo`, `PaginationReq`, `SortField` |
| `container.go` | `ParameterItem`, `HelmConfigItem` (+`NewHelmConfigItem`), `ContainerVersionItem` (+`NewContainerVersionItem`), `ContainerRef`, `ContainerSpec`, `ParameterSpec`, `BuildOptions` |
| `dataset.go` | `DatasetRef` |
| `dynamic_config.go` | `ConfigUpdateResponse` (+`NewConfigUpdateResponse`) |
| `injection.go` | `InjectionItem` (+`NewInjectionItem`) |
| `label.go` | `LabelItem`, `ConvertLabelItemsToConditions` |
| `log.go` | `LogEntry` |
| `permission.go` | `CheckPermissionParams` (consumed by middleware/permission.go) |
| `project.go` | `ProjectStatistics` |
| `rate_limiter.go` | `RateLimiterHolder`, `RateLimiterItem`, `RateLimiterListResp`, `RateLimiterGCResp` |
| `response.go` | `GenericResponse[T]`, `GenericCreateResponse[T,E]`, `JSONResponse`, `SuccessResponse`, `ErrorResponse` — **core plumbing** |
| `search.go` | `SortDirection`, `FilterOperator`, `SearchFilter`, `SortOption`, `TypedSortOption[F]`, `SearchReq[F]`, `DateRange`, `NumberRange`, `AdvancedSearchReq[F]`, `AlgorithmSearchReq`, `ListResp[T]`, `SearchGroupNode[T]`, `SearchResp[T]`, `BuildGroupTree` — **heavily used by searchx.QueryBuilder** |
| `task.go` | `RetryPolicy`, `UnifiedTask` |
| `trace.go` | `TraceStreamEvent`, `DatapackInfo`, `DatapackResult`, `ExecutionInfo`, `ExecutionResult`, `InfoPayloadTemplate`, `TaskScheduledPayload`, `JobMessage` |

dto/ is **not** a deprecated shell. It contains the common base types that every module's api_types.go composes on top of. However note: `dto.ContainerSpec` (container.go:169) and `dto.ContainerRef` are grep-referenced by multiple modules; no migration planned per the code.

---

## 6. consts/

| File | Main exports |
|---|---|
| `consts.go` (574 ln) | `InitialFilename`, `DetectorKey`, `Hybrid` FaultType sentinel; enums `DatapackSource`, `LabelCategory`, `AuditLogState`, `BuildSourceType`, `ConfigHistoryChangeType/Field`, `ContainerType`, `ConfigScope`, `ConfigValueType`, `ParameterType`, `ParameterCategory`, `ValueDataType`, `DatapackState`, `ExecutionState`, `FaultType`, `GrantType` (grant/deny), `PageSize`, `TaskExtra`, `TaskState`, `TraceType`, `TraceState`, `TaskType`, `StatusType` (−1/0/1); event-name consts `EventType` (41 variants including `EventJobSucceed/Failed`, `EventDatapackNoAnomaly`, etc.); `VolumeMountName`, `SSEEventName`, `LogLevel`, `WSLogType`; span keys `TaskIDKey/TypeKey/StateKey`; **`URLPathX` constants** (line 550-571) used by permission.go and handlers; globals `AppID`, `InitialTime` |
| `collection.go` (372 ln) | enum→name string maps + `GetXName`/`GetXByName` helpers; `SystemRoleDisplayNames` (line 154) |
| `errors.go` (10 ln) | 6 sentinel errors: `ErrAuthenticationFailed`, `ErrPermissionDenied`, `ErrNotFound`, `ErrAlreadyExists`, `ErrBadRequest`, `ErrInternal` — consumed by `httpx.HandleServiceError` (common.go:66-91) to map to HTTP status |
| `export.go` (21 ln) | string wrapper types `DatapackStateString`, `ExecutionStateString` (for CSV export?) |
| `search_fields.go` (53 ln) | `InjectionField` + `InjectionAllowedFields` map; `DatasetField` + `DatasetAllowedFields` map — passed into `searchx.NewQueryBuilder` |
| `system.go` (579 ln) | `ActionName` (19 consts: create/read/update/delete/execute/stop/restart/activate/suspend/upload/download/import/export/assign/grant/revoke/configure/manage/share/clone/monitor/analyze/audit); `ResourceName` (17 consts including `ResourceLabel`, `ResourceTeam`); `ProjectScopedResources`, `TeamScopedResources` + `IsProjectScoped/IsTeamScoped`; `ResourceType`, `ResourceCategory`, `ResourceScope` (own/project/team/all); `RoleName` (16: super_admin, admin, user, container_admin/developer/viewer, dataset_*, project_admin/algo_developer/data_developer/viewer, team_admin/member/viewer); `PermissionRule{Resource,Action,Scope}` + `ParsePermissionRule`/`String`; **`PermXxx` predefined rules** (line 204-346, ~80 rules); `SystemRolePermissions` map (line 349) — seed data for RBAC bootstrap |
| `validation.go` (224 ln) | 20+ `ValidX` whitelist maps used by request validation; includes `ValidTaskTypes`, `ValidTraceTypes`, etc. |

**New since pre-refactor**:
- `system.go`: `RoleTeamAdmin/Member/Viewer` + `PermTeam*` rules + `ProjectScopedResources`/`TeamScopedResources` classification.
- No explicit "API key scope enum" — scopes are strings (`"sdk:evaluations:read"`) evaluated by `api_key_scope.go` glob matcher.

---

## 7. utils/

19 files (same count as pre-refactor, 1 new, 1 removed vs pre-refactor list).

- **`access_key_crypto.go` (NEW, 83 ln)** — AES-256-GCM + HMAC-SHA256 for API-key secrets. Key derivation: `apiKeyCryptoKey()` = `sha256(JWTSecret)` (line 80-83). Funcs: `EncryptAPIKeySecret`/`DecryptAPIKeySecret` (GCM seal/open over base64), `SignAPIKeyRequest`/`VerifyAPIKeyRequestSignature` (hmac-sha256 hex), `SHA256Hex`. **Security note**: key is derived from the hard-coded `JWTSecret="your-secret-key-change-this-in-production"` in utils/jwt.go:16 — compromising JWT signing key also decrypts every API key ciphertext (see Surprises).
- `jwt.go` — now stores `IsAdmin`, `Roles`, `AuthType`, `APIKeyID`, `APIKeyScopes` in `Claims`; `ServiceClaims{TaskID}`; `GenerateAPIKeyToken` (line 54) uses `auth_type="api_key"`; `GenerateServiceToken` for K8s jobs. Hardcoded `JWTSecret`, `TokenExpiration=24h`, `RefreshTokenExpiration=7d`, `ServiceTokenExpiration=24h`.
- Unchanged: `color.go`, `docker.go`, `error.go` (ErrorProcessor used by httpx), `fault_translate.go` (chaos-experiment integration), `file.go`, `github.go`, `map.go`, `password.go` (+`password_test.go`), `pointer.go`, `runtime.go`, `slice.go`, `string.go` (`ToSingular` used by audit resource inference), `type.go`, `validator.go` (`IsValidEnvVar`, `IsValidHelmValueKey`, `ParseSemanticVersion`, `ParseFullImageRefernce`), `version.go`.

No removed files vs pre-refactor list. No standalone CORS/HTTP helper (CORS is wired in router.go directly).

---

## 8. tracing/, httpx/, searchx/

- **`tracing/decorator.go` (56 ln)** — thin OTEL wrapper. Exports `WithSpan(ctx, f)` (auto-names from `runtime.Caller`), `WithSpanNamed(ctx, name, f)`, `WithSpanReturnValue[T]`, `SetSpanAttribute`. Single tracer name `rcabench/task`. Used by consumers to wrap service calls.
- **`httpx/common.go` (94 ln)** — `ParsePositiveID(c, idStr, fieldName) (int, bool)`; `HandleServiceError(c, err)` maps `consts.ErrX` sentinels to HTTP codes via `utils.NewErrorProcessor` (innermost error + optional user-friendly wrapper — see utils/error.go). This is the canonical handler-side error translator.
- **`httpx/request_id.go` (119 ln)** — `X-Request-Id` propagation: HTTP header ↔ context value ↔ grpc metadata. `NewRequestID`/`WithRequestID`/`RequestIDFromContext`/`WithOutgoingRequestID`; `UnaryClientRequestIDInterceptor`/`UnaryServerRequestIDInterceptor` for gRPC.
- **`searchx/query_builder.go` (248 ln)** — replaces old `repository/query_builder.go`. `QueryBuilder[F ~string]` generic over field-enum type. API:
  - `NewQueryBuilder(db, allowedSortFields map[F]string) *QueryBuilder[F]` (line 21)
  - `ApplySearchReq(filters, keyword, sortOptions, groupBy, modelType) *gorm.DB` (line 29) — applies filters + keyword + sort in one call
  - `ApplyIncludes(includes []string)` (line 39) — Preload list
  - `ApplyIncludeFields(fields)` / `ApplyExcludeFields(fields, modelType)` (line 45-85) — column projection via reflection on `gorm` tag
  - Plus internal `applyFilters`, `applyKeywordSearch`, `applySorting` (not inspected in detail).
  - **API change from pre-refactor**: generified over `F ~string` (typed field enum). Previously it was untyped `string` per the prior recovery note. The `dto.TypedSortOption[F]` / `dto.SearchReq[F]` also generified. Callers pass `consts.InjectionField` / `consts.DatasetField` as `F`.

---

## 10. Frontend → backend `/api/v2/*` usage

Base path is `BASE_PATH = '/api/v2'` in `AegisLab-frontend/src/api/client.ts:4`. All hand-rolled modules call `apiClient.get('/xxx')` relative. There are also `@rcabench/client` SDK calls (auth.ts, sdk.ts) that go through the same axios via `sdkAxios`, but those target whatever paths the generated SDK encodes (separate concern).

**Deduped endpoint path list** (relative paths, alpha-sorted). All live under the implicit `/api/v2/` prefix. Parametrised segments shown as `{id}` / `{name}`:

```
auth:
  POST /auth/refresh                              (client.ts interceptor)
containers:
  GET|POST   /containers
  GET|DELETE /containers/{id}
  PATCH      /containers/{id}/labels
  POST       /containers/build
datasets:
  POST|DELETE /datasets, /datasets/{id}
  PATCH       /datasets/{id}/labels
  GET         /datasets/{id}/versions/{ver}/download    (fileClient)
evaluations:
  DELETE /evaluations/{id}
  POST   /evaluations/datapacks
  POST   /evaluations/datasets
executions:
  GET   /executions/labels
  PATCH /executions/{id}/labels
  POST  /executions/batch-delete
groups:
  GET /groups/{id}/stats
  SSE /api/v2/groups/{id}/stream?token=...        (groups.ts:16 — absolute URL for EventSource)
injections:
  GET   /injections
  POST  /injections/search
  GET   /injections/{id}
  GET   /injections/metadata
  POST  /injections/{id}/clone
  POST  /injections/batch-delete
  GET   /injections/{id}/files
  GET   /injections/{id}/files/download            (fileClient)
  GET   /injections/{id}/download                  (fileClient)
  POST  /injections/upload                         (multipart)
  GET   /injections/{id}/files/query
labels:
  GET|POST     /labels
  GET|PATCH|DELETE /labels/{id}
  POST         /labels/batch-delete
metrics:
  GET /metrics/algorithms
  GET /metrics/executions
  GET /metrics/injections
permissions:
  GET /permissions
  GET /permissions/{id}
projects:
  PATCH /projects/{id}/labels                     (only labels mutation surfaced; rest uses SDK)
resources:
  GET /resources
  GET /resources/{id}
  GET /resources/{id}/permissions
roles:
  GET|POST /roles
  GET|PATCH|DELETE /roles/{id}
  GET /roles/{id}/users
system:
  GET /system/metrics
  GET /system/metrics/history
tasks:
  GET /tasks
  GET /tasks/{taskId}
  POST /tasks/batch-delete
  WS  /api/v2/tasks/{taskId}/logs/ws?token=...    (tasks.ts:40 — window.location.host abs URL)
teams:
  GET|POST /teams
  GET|PATCH|DELETE /teams/{id}
  GET  /teams/{id}/members, /teams/{id}/projects
  POST /teams/{id}/members
  DELETE /teams/{id}/members/{userId}
traces:
  GET /traces
  GET /traces/{traceId}
  SSE /api/v2/traces/{traceId}/stream?token=...   (traces.ts:21, useTraceSSE.ts:221)
users:
  GET /users
  GET /users/{id}/detail
  POST /users
  PATCH|DELETE /users/{id}
  POST /users/{userId}/roles/{roleId}
  DELETE /users/{userId}/roles/{roleId}
```

**SSE endpoints referenced by the frontend** (literal):
- `/api/v2/groups/{groupId}/stream` (groups.ts:16)
- `/api/v2/traces/{traceId}/stream` (traces.ts:21, useTraceSSE.ts:221)
- Missing: the backend exposes `/notifications/stream` (portal.go:151) — **no frontend caller**.

**WebSocket endpoints**:
- `/api/v2/tasks/{taskId}/logs/ws` (tasks.ts:40)

**Audience prefix reality check**:
- `/admin/*` — the backend does NOT use an `/admin/` URL segment. Admin routes live under e.g. `/users`, `/roles`, `/resources`, `/systems`, `/system`. Frontend calls all of these directly, no `/admin/` prefix on either side. The "admin router" is audience-only, URL-transparent.
- `/portal/*` — same: no such URL prefix on the backend. Frontend calls flat URLs.
- `/public/*` — same: only the `/auth/*` surface.
- `/sdk/*` — backend exposes `/sdk/evaluations/*` and `/sdk/datasets` (sdk.go:15,22). **Frontend does NOT call `/sdk/*`** (grep confirms zero matches). Only non-SDK `/evaluations/*` is called.

**Conclusion**: the audience split is purely a code-organisation concern. Frontend paths didn't need to change because URLs remained flat under `/api/v2/`. The frontend has not caught up to using the SDK endpoints deliberately (e.g. `/evaluations/datapacks` is called from portal-style `apiClient` but is wired as SDK in the backend). This is a deduped view, not a spec conflict.

---

## 11. rcabench-platform datapack file contract

rcabench-platform reads a datapack folder (per `rcabench-platform/CLAUDE.md:132-146` + `.github/copilot-instructions.md:96-108` + source scripts):

```
<datapack>/
├── trace.parquet          — distributed traces
├── log.parquet            — app logs
├── metrics.parquet        — time-series metrics
├── metrics_sli.parquet    — SLI metrics with anomaly detection
├── injection.json         — ground-truth fault injection info
```

Evidence from actual Python code:
- `cli/dataset_analysis/detector_cli.py:98` — `injection_path = p / "injection.json"`
- `cli/dataset_analysis/detector_cli.py:208` — `batch_dir / "trace.parquet"`
- `cli/dataset_analysis/scan_datasets.py:40-50` — reads `traces.parquet`, `simple_metrics.parquet`, `normal_traces.parquet`, `abnormal_traces.parquet`, `normal_metrics.parquet`, `abnormal_metrics.parquet`, `normal_logs.parquet`, `abnormal_logs.parquet`
- `notebooks/sdg.py:135` — `_datapack_folder / "injection.json"`

Also platform meta files (written by platform itself, not AegisLab):
- `index.parquet` (dataset-level datapack listing), `labels.parquet`, `conclusion.parquet` (detector output), `perf.parquet`, `ranks.parquet` (algorithm output).

**AegisLab seam**: AegisLab writes into JuiceFS `dataset_path` the datapack folder with `trace.parquet`/`log.parquet`/`metrics.parquet`/`metrics_sli.parquet`/`injection.json`. Backend code references: `consts/consts.go:531-532` constants `DetectorConclusionFile="conclusion.csv"`, `ExecutionResultFile="result.csv"` — those are AegisLab → platform **output** files, not the datapack contract itself. Confirmed: datapack input contract is the 5 files above; no newer/deprecated filenames found.

---

## 12. chaos-experiment import surface

Top-level dirs in `/home/ddq/AoyangSpace/aegis/chaos-experiment/`: `chaos`, `client`, `cmd`, `controllers`, `docs`, `handler`, `internal`, `pkg`, `tools`, `utils`.

AegisLab imports (deduped by subpackage → file):

| Subpackage | Files importing it (relative to src/) |
|---|---|
| `chaos-experiment/handler` (aliased `chaos`) | consts/consts.go:8; utils/fault_translate.go:10, fault_translate_test.go:7; model/entity.go:10, entity_helper.go:8, view.go:6; config/chaos_system.go:9; service/initialization/{producer.go:21, systems.go:10}; service/common/metadata_store.go:10; service/consumer/{fault_injection.go:18, jvm_runtime_mutator.go:14, restart_pedestal.go:19}; module/evaluation/api_types.go:12; module/injection/{api_types.go:15, handler.go:24, resolve_specs_test.go:8, runtime_types.go:10, service.go:23, spec_convert.go:12, spec_convert_test.go:7, submit.go:13}; module/dataset/api_types.go:14; module/execution/{api_types.go:13, service.go:19}; module/chaossystem/service.go:12 |
| `chaos-experiment/client` (aliased `chaosCli`) | infra/chaos/module.go:4; infra/k8s/controller.go:11 |
| `chaos-experiment/pkg/guidedcli` | cmd/aegisctl/cmd/inject_guided.go:12; service/consumer/fault_injection.go:19; module/injection/{api_types.go:16, submit.go:14} |

**Used top-level dirs**: `handler`, `client`, `pkg/guidedcli` — **exactly the same three as the pre-refactor recovery noted**. Not used: `chaos`, `cmd`, `controllers`, `docs`, `internal`, `tools`, `utils`.

---

## 13. Surprises

1. **`middleware.AuditMiddleware()` is defined but NOT wired in `router.New`**. The global chain at router/router.go:25-32 includes only InjectService/RequestID/GroupID/SSEPath/CORS/Tracer. Audit logging is silently disabled despite full `dbBackedMiddlewareService.LogUserAction/LogFailedAction` impls in deps.go.
2. **Rate-limit middleware bucket vars (`GeneralRateLimit`, `AuthRateLimit`, `StrictRateLimit`, `UserRateLimit`) are declared but not mounted on any route**. Only `module/ratelimiter` (Redis) is real.
3. **JWT secret is hard-coded**: `utils/jwt.go:16` `JWTSecret = "your-secret-key-change-this-in-production"`. Worse, `utils/access_key_crypto.go:81` derives the AES-GCM key for API-key ciphertext from the SAME JWTSecret via `sha256(JWTSecret)`. So committing the literal to git also leaks every user's API key secret stored in `api_keys.key_secret_ciphertext`.
4. **CORS is wide-open**: `AllowAllOrigins=true` + `AllowCredentials=true` (router.go:18-23). The gin-contrib/cors lib typically rejects this combo, so requests may silently fail in browser or fall back to `*`. Worth validating in prod.
5. **`SSEPath()` middleware regex `^/stream(/.*)?$`** (middleware.go:23) never matches actual SSE routes `/traces/:id/stream`, `/groups/:id/stream`, `/notifications/stream` because those don't start with `/stream`. Response headers therefore never get set by this middleware — they must be set by handlers themselves or it's a silent bug.
6. **`/notifications/stream` endpoint exists on backend (portal.go:151) but has no frontend caller**. Either dead code or awaiting frontend wiring.
7. **Frontend does not use the `/sdk/*` endpoints** even though they exist (sdk.go:15,22). The frontend calls `/evaluations/*`, `/executions/labels`, `/metrics/*` directly — these are dual-mounted with API-key scope middleware that user JWTs bypass. Effectively, the `/sdk/*` tree is machine-only and untested from UI.
8. **`consts.ValidTaskTypes` (validation.go:203) is declared but not found to be referenced by any code importing `aegis/consts`** — typical stale whitelist. Same with several other `ValidX` maps in validation.go.
9. **`auth_type` header is determined purely by JWT claim**. `RequireAPIKeyScopesAny` short-circuits (`c.Next()`) when `auth_type != "api_key"` — so any user-token bearer bypasses SDK scope checks entirely. This is intentional (per comment), but means the scope enforcement is at the **token-exchange** surface only.
10. **CORS `ExposeHeaders` omits `X-Group-ID`** (router.go:22) even though `GroupID()` middleware writes it. Browser JS reading `res.headers.get('X-Group-Id')` will see `null` unless the server also exposes it. (Only `X-Request-Id` is exposed.)
