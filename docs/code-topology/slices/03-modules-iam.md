# Slice 03 — IAM + platform core modules

> Archival note: this file was not fully revalidated after the phase-2 gRPC collapse and phase-6 module-wiring cleanup. Treat `docs/code-topology/README.md`, `docs/code-topology/slices/01-app-wiring.md`, and `docs/code-topology/slices/06-grpc-interfaces.md` as the current topology source of truth.

Code paths relative to `/home/ddq/AoyangSpace/aegis/aegislab/src/`.
All citations are `file:line`.

---

## 1. Per-module summary

### module/auth
- Files: `module.go`, `api_types.go`, `handler.go`, `handler_service.go`, `middleware_adapter.go`, `repository.go`, `service.go`, `token_store.go`, `service_test.go`
- fx.Module wiring (`module/auth/module.go:7-16`):
  - Provide: `NewUserRepository`, `NewRoleRepository`, `NewAPIKeyRepository`, `NewTokenStore`, `NewService`, `AsHandlerService`, `NewTokenVerifier`, `NewHandler`
- HandlerService (`module/auth/handler_service.go:10-26`):
  - `Login(ctx, *LoginReq) (*LoginResp, error)`
  - `Register(ctx, *RegisterReq) (*UserInfo, error)`
  - `RefreshToken(ctx, *TokenRefreshReq) (*TokenRefreshResp, error)`
  - `Logout(ctx, *utils.Claims) error`
  - `ChangePassword(ctx, *ChangePasswordReq, int) error`
  - `GetProfile(ctx, int) (*UserProfileResp, error)`
  - `CreateAPIKey(ctx, int, *CreateAPIKeyReq) (*APIKeyWithSecretResp, error)`
  - `ListAPIKeys(ctx, int, *ListAPIKeyReq) (*ListAPIKeyResp, error)`
  - `GetAPIKey(ctx, int, int) (*APIKeyInfo, error)`
  - `DeleteAPIKey/DisableAPIKey/EnableAPIKey/RevokeAPIKey(ctx, int, int) error`
  - `RotateAPIKey(ctx, int, int) (*APIKeyWithSecretResp, error)`
  - `ExchangeAPIKeyToken(ctx, *APIKeyTokenReq, method, path) (*APIKeyTokenResp, error)`
- Handler gin routes (`module/auth/handler.go`):
  - `Login`        → `POST /api/v2/auth/login` (L38)
  - `Register`     → `POST /api/v2/auth/register` (L73)
  - `RefreshToken` → `POST /api/v2/auth/refresh` (L108)
  - `Logout`      → `POST /api/v2/auth/logout` (L141)
  - `ChangePassword` → `POST /api/v2/auth/change-password` (L179)
  - `GetProfile`  → `GET /api/v2/auth/profile` (L218)
  - `CreateAPIKey`  → `POST /api/v2/api-keys` (L249)
  - `ListAPIKeys`   → `GET /api/v2/api-keys` (L290)
  - `GetAPIKey`     → `GET /api/v2/api-keys/{id}` (L330)
  - `DeleteAPIKey`  → `DELETE /api/v2/api-keys/{id}` (L359)
  - `DisableAPIKey` → `POST /api/v2/api-keys/{id}/disable` (L387)
  - `EnableAPIKey`  → `POST /api/v2/api-keys/{id}/enable` (L415)
  - `RevokeAPIKey`  → `POST /api/v2/api-keys/{id}/revoke` (L443)
  - `RotateAPIKey`  → `POST /api/v2/api-keys/{id}/rotate` (L471)
  - `ExchangeAPIKeyToken` → `POST /api/v2/auth/api-key/token` (L502)
- Service struct (`module/auth/service.go:22-36`): fields `userRepo`, `roleRepo`, `apiKeyRepo`, `tokenStore`. Exported methods: the whole HandlerService surface + `VerifyToken(ctx, token) (*utils.Claims, error)` (L172) + `VerifyServiceToken(ctx, token) (*utils.ServiceClaims, error)` (L191). Uses `user.NewUserContainerInfo/NewUserDatasetInfo/NewUserProjectInfo` for `getAllUserResourceRoles` (L526).
- Repository (`module/auth/repository.go`): three DAOs in one file:
  - `UserRepository` on `model.User` — `Create`, `GetByID`, `GetByUsername`, `GetByEmail`, `Update`, `UpdateLoginTime`, `ListContainerRoles`, `ListDatasetRoles`, `ListProjectRoles` (returns `UserContainer/UserDataset/UserProject` preloaded with Role).
  - `RoleRepository` on `model.Role` — `ListByUserID` (JOIN user_roles).
  - `APIKeyRepository` on `model.APIKey` — `Create`, `ListByUserID`, `GetByIDForUser`, `GetByKeyID`, `Update`, `UpdateLastUsedAt`.
- Store (`module/auth/token_store.go`): `TokenStore` wraps `infra/redis.Gateway`. Keys: `blacklist:token:<jwt id>` (`SET` w/ TTL until token expiry, value = metadata JSON); `api_key:nonce:<keyID>:<nonce>` (`SETNX` TTL for replay protection). Methods: `AddTokenToBlacklist`, `IsTokenBlacklisted`, `ReserveAPIKeyNonce`.
- Cross-module deps: `module/user` (resource info DTOs in GetProfile), `infra/redis`, `middleware` (for TokenVerifier adapter + `GetCurrentUserID` in handler).
- Notable per-module files:
  - `middleware_adapter.go`: single function `NewTokenVerifier(*Service) middleware.TokenVerifier` — satisfies `middleware.TokenVerifier` (VerifyToken + VerifyServiceToken) so `middleware.NewService` can consume it.
  - `token_store.go`: Redis-backed JWT blacklist + API-key-exchange nonce reservation.

### module/rbac
- Files: `module.go`, `api_types.go`, `handler.go`, `handler_service.go`, `repository.go`, `service.go`, `service_test.go`
- fx.Module (`module/rbac/module.go:5-10`): Provide `NewRepository`, `NewService`, `AsHandlerService`, `NewHandler`.
- HandlerService (`module/rbac/handler_service.go:10-25`):
  - `CreateRole(ctx, *CreateRoleReq) (*RoleResp, error)`
  - `DeleteRole(ctx, int) error`
  - `GetRole(ctx, int) (*RoleDetailResp, error)`
  - `ListRoles(ctx, *ListRoleReq) (*dto.ListResp[RoleResp], error)`
  - `UpdateRole(ctx, *UpdateRoleReq, int) (*RoleResp, error)`
  - `AssignRolePermissions(ctx, []int, int) error`
  - `RemoveRolePermissions(ctx, []int, int) error`
  - `ListUsersFromRole(ctx, int) ([]UserListItem, error)`
  - `GetPermission(ctx, int) (*PermissionDetailResp, error)`
  - `ListPermissions(ctx, *ListPermissionReq) (*dto.ListResp[PermissionResp], error)`
  - `ListRolesFromPermission(ctx, int) ([]RoleResp, error)`
  - `GetResource(ctx, int) (*ResourceResp, error)`
  - `ListResources(ctx, *ListResourceReq) (*dto.ListResp[ResourceResp], error)`
  - `ListResourcePermissions(ctx, int) ([]PermissionResp, error)`
- Handler gin routes (`module/rbac/handler.go`):
  - `CreateRole`  → `POST /api/v2/roles` (L40)
  - `DeleteRole`  → `DELETE /api/v2/roles/{id}` (L70)
  - `GetRole`     → `GET /api/v2/roles/{id}` (L98)
  - `ListRoles`   → `GET /api/v2/roles` (L129)
  - `UpdateRole`  → `PATCH /api/v2/roles/{id}` (L161)
  - `AssignRolePermissions` → `POST /api/v2/roles/{role_id}/permissions/assign` (L201)
  - `RemoveRolePermissions` → `POST /api/v2/roles/{role_id}/permissions/remove` (L236)
  - `ListUsersFromRole`     → `GET /api/v2/roles/{role_id}/users` (L269)
  - `GetPermission`         → `GET /api/v2/permissions/{id}` (L298)
  - `ListPermissions`       → `GET /api/v2/permissions` (L330)
  - `ListRolesFromPermission` → `GET /api/v2/permissions/{permission_id}/roles` (L364)
  - `GetResource`            → `GET /api/v2/resources/{id}` (L393)
  - `ListResources`          → `GET /api/v2/resources` (L424)
  - `ListResourcePermissions` → `GET /api/v2/resources/{id}/permissions` (L458)
- Service (`module/rbac/service.go:15-21`) has single field `repo *Repository`; every op runs within `s.repo.db.Transaction`, delegating to tx-scoped `NewRepository(tx)`.
- Repository (`module/rbac/repository.go`): entities `model.Role`, `model.Permission`, `model.Resource`, `model.RolePermission`, `model.UserRole`, `model.UserContainer/UserDataset/UserProject` (all touched in `deleteRoleCascade`). Exported method: only `NewRepository`; all ops unexported (`createRoleRecord`, `deleteRoleCascade`, `loadRoleDetail`, `listRoleViews`, `updateMutableRole`, `assignRolePermissions`, `removeRolePermissions`, `listUsersFromRole`, `getPermissionDetail`, `listPermissionViews`, `listRolesFromPermission`, `getResourceDetail`, `listResourceViews`, `listResourcePermissions`, `loadRole`, `listPermissionsByIDs`, `buildAssignablePermissionMap`).
- Stores: none.
- Cross-module deps: `consts`, `dto`, `model` only. No other module imports.

### module/user
- Files: `module.go`, `api_types.go`, `handler.go`, `handler_service.go`, `repository.go`, `service.go`, `service_test.go`
- fx.Module (`module/user/module.go:5-10`): Provide `NewRepository`, `NewService`, `AsHandlerService`, `NewHandler`.
- HandlerService (`module/user/handler_service.go:10-26`):
  - `CreateUser(ctx, *CreateUserReq) (*UserResp, error)`
  - `DeleteUser(ctx, int) error`
  - `GetUserDetail(ctx, int) (*UserDetailResp, error)`
  - `ListUsers(ctx, *ListUserReq) (*dto.ListResp[UserResp], error)`
  - `UpdateUser(ctx, *UpdateUserReq, int) (*UserResp, error)`
  - `AssignRole(ctx, int, int) error` / `RemoveRole(ctx, int, int) error`
  - `AssignPermissions(ctx, *AssignUserPermissionReq, int) error`
  - `RemovePermissions(ctx, *RemoveUserPermissionReq, int) error`
  - `AssignContainer(ctx, int, int, int) error` / `RemoveContainer(ctx, int, int) error`
  - `AssignDataset(ctx, int, int, int) error` / `RemoveDataset(ctx, int, int) error`
  - `AssignProject(ctx, int, int, int) error` / `RemoveProject(ctx, int, int) error`
- Handler gin routes (`module/user/handler.go`):
  - `CreateUser`   → `POST /api/v2/users` (L38)
  - `DeleteUser`   → `DELETE /api/v2/users/{id}` (L75)
  - `GetUserDetail` → `GET /api/v2/users/{id}/detail` (L103)
  - `ListUsers`    → `GET /api/v2/users` (L136)
  - `UpdateUser`   → `PATCH /api/v2/users/{id}` (L172)
  - `AssignRole`   → `POST /api/v2/users/{user_id}/role/{role_id}` (L207)
  - `RemoveRole`   → `DELETE /api/v2/users/{user_id}/roles/{role_id}` (L236)
  - `AssignPermissions` → `POST /api/v2/users/{user_id}/permissions/assign` (L266)
  - `RemovePermissions` → `POST /api/v2/users/{user_id}/permissions/remove` (L305)
  - `AssignContainer`   → `POST /api/v2/users/{user_id}/containers/{container_id}/roles/{role_id}` (L344)
  - `RemoveContainer`   → `DELETE /api/v2/users/{user_id}/containers/{container_id}` (L381)
  - `AssignDataset`     → `POST /api/v2/users/{user_id}/datasets/{dataset_id}/roles/{role_id}` (L415)
  - `RemoveDataset`     → `DELETE /api/v2/users/{user_id}/datasets/{dataset_id}` (L452)
  - `AssignProject`     → `POST /api/v2/users/{user_id}/projects/{project_id}/roles/{role_id}` (L486)
  - `RemoveProject`     → `DELETE /api/v2/users/{user_id}/projects/{project_id}` (L523)
- Service (`module/user/service.go:16-22`): one field `repo *Repository`; wraps everything in tx. Uses `rbac.NewRoleResp` / `rbac.NewPermissionResp` when hydrating `UserDetailResp` (L86-94).
- Repository (`module/user/repository.go`): all unexported DAO methods on `model.User`, `model.UserRole`, `model.UserPermission`, `model.UserContainer`, `model.UserDataset`, `model.UserProject`, `model.Permission`:
  `createUserIfUnique`, `getUserDetailBase`, `deleteUserCascade`, `listUserViews`, `updateMutableUser`, `loadUserDetailRelations`, `assignGlobalRole`, `removeGlobalRole`, `buildUserPermissions`, `batchCreateUserPermissions`, `batchDeleteUserPermissions`, `assignContainerRole`/`removeContainerRole`, `assignDatasetRole`/`removeDatasetRole`, `assignProjectRole`/`removeProjectRole`, `ensureActiveRecordExists`, `listPermissionsByIDs`.
- Stores: none.
- Cross-module deps: `module/rbac` (DTO hydration), `consts`, `dto`, `model`.

### module/team
- Files: `module.go`, `api_types.go`, `handler.go`, `handler_service.go`, `repository.go`, `service.go`, `project_reader.go`, `service_test.go`
- fx.Module (`module/team/module.go:5-11`): Provide `NewRepository`, `newProjectReader`, `NewService`, `AsHandlerService`, `NewHandler`.
- HandlerService (`module/team/handler_service.go:10-21`):
  - `CreateTeam(ctx, *CreateTeamReq, userID int) (*TeamResp, error)`
  - `DeleteTeam(ctx, int) error`
  - `GetTeamDetail(ctx, int) (*TeamDetailResp, error)`
  - `ListTeams(ctx, *ListTeamReq, userID int, isAdmin bool) (*dto.ListResp[TeamResp], error)`
  - `UpdateTeam(ctx, *UpdateTeamReq, int) (*TeamResp, error)`
  - `ListTeamProjects(ctx, *TeamProjectListReq, teamID int) (*dto.ListResp[TeamProjectItem], error)`
  - `AddMember(ctx, *AddTeamMemberReq, teamID int) error`
  - `RemoveMember(ctx, teamID, currentUserID, targetUserID int) error`
  - `UpdateMemberRole(ctx, *UpdateTeamMemberRoleReq, teamID, targetUserID, currentUserID int) error`
  - `ListMembers(ctx, *ListTeamMemberReq, teamID int) (*dto.ListResp[TeamMemberResp], error)`
- Handler gin routes (`module/team/handler.go`):
  - `CreateTeam`     → `POST /api/v2/teams` (L41)
  - `DeleteTeam`     → `DELETE /api/v2/teams/{team_id}` (L80)
  - `GetTeamDetail`  → `GET /api/v2/teams/{team_id}` (L108)
  - `ListTeams`      → `GET /api/v2/teams` (L139)
  - `UpdateTeam`     → `PATCH /api/v2/teams/{team_id}` (L180)
  - `ListTeamProjects` → `GET /api/v2/teams/{team_id}/projects` (L222)
  - `AddTeamMember`    → `POST /api/v2/teams/{team_id}/members` (L263)
  - `RemoveTeamMember` → `DELETE /api/v2/teams/{team_id}/members/{user_id}` (L301)
  - `UpdateTeamMemberRole` → `PATCH /api/v2/teams/{team_id}/members/{user_id}/role` (L345)
  - `ListTeamMembers` → `GET /api/v2/teams/{team_id}/members` (L393)
- Service (`module/team/service.go:15-25`): fields `repo *Repository`, `projects projectReader`. Beyond HandlerService it also exports `IsUserInTeam`, `IsUserTeamAdmin`, `IsTeamPublic` (L175/186/197) — currently unused by `middleware` (which uses its own `dbBackedMiddlewareService`).
- Repository (`module/team/repository.go`): on `model.Team`, `model.UserTeam`, plus read-only joins to `model.Project`; methods `createTeamWithCreator`, `loadTeamDetailBase`, `listVisibleTeams`, `updateMutableTeam`, `listTeamProjectViews`, `addMember`, `removeMember`, `updateMemberRole`, `listTeamMembers`, `loadUserTeamMembership`, `deleteTeam`, `isTeamPublic`, `loadTeam`.
- Notable: `project_reader.go` — `projectReader` abstraction with `CountProjects`/`ListProjects`. Local path reads `model.Project` via GORM; remote path talks to `internalclient/resourceclient.Client.ListProjects`. `RemoteProjectReaderOption()` decorates to require-remote mode (used by the dedicated `iam-service` app).
- Cross-module deps: `module/project` (DTO reuse: `TeamProjectListReq = project.ListProjectReq`, `TeamProjectItem = project.ProjectResp`; `project.NewProjectResp` hydration), `internalclient/resourceclient`, `middleware` (handler: `GetCurrentUserID`, `IsAdmin`), `consts`, `dto`, `model`.

### module/project
- Files: `module.go`, `api_types.go`, `handler.go`, `handler_service.go`, `repository.go`, `service.go`, `project_statistics.go`, `service_test.go`
- fx.Module (`module/project/module.go:7-15`): Provide `NewRepository`, `newProjectStatisticsSource`, `NewService`, `AsHandlerService`, `NewHandler`.
- HandlerService (`module/project/handler_service.go:10-17`):
  - `CreateProject(ctx, *CreateProjectReq, userID int) (*ProjectResp, error)`
  - `DeleteProject(ctx, int) error`
  - `GetProjectDetail(ctx, int) (*ProjectDetailResp, error)`
  - `ListProjects(ctx, *ListProjectReq) (*dto.ListResp[ProjectResp], error)`
  - `UpdateProject(ctx, *UpdateProjectReq, int) (*ProjectResp, error)`
  - `ManageProjectLabels(ctx, *ManageProjectLabelReq, int) (*ProjectResp, error)`
- Handler gin routes (`module/project/handler.go`):
  - `CreateProject`    → `POST /api/v2/projects` (L41)
  - `DeleteProject`    → `DELETE /api/v2/projects/{project_id}` (L84)
  - `GetProjectDetail` → `GET /api/v2/projects/{project_id}` (L115)
  - `ListProjects`     → `GET /api/v2/projects` (L148)
  - `UpdateProject`    → `PATCH /api/v2/projects/{project_id}` (L187)
  - `ManageProjectCustomLabels` → `PATCH /api/v2/projects/{project_id}/labels` (L231)
- Service (`module/project/service.go:16-26`): fields `repository *Repository`, `stats projectStatisticsSource`. Also exports `Repository.ListProjectStatistics(projectIDs []int) (map[int]*dto.ProjectStatistics, error)` — unexported repo otherwise.
- Repository (`module/project/repository.go`): on `model.Project`, `model.UserProject`, `model.ProjectLabel`; unexported `createProjectWithOwner`, `deleteProjectCascade`, `loadProjectDetailBase`, `listProjectViews`, `updateMutableProject`, `manageProjectLabels`, `loadProjectRecord`; exported `ListProjectStatistics` (so the remote-statistics adapter can still call it).
- Notable: `project_statistics.go` — `projectStatisticsSource` abstraction. Local path uses the repo; remote path uses `internalclient/orchestratorclient.Client.ListProjectStatistics`. `RemoteStatisticsOption()` provides `fx.Decorate` to the remote-only adapter for the dedicated `resource-service` app.
- Cross-module deps: `module/label` (for `label.NewRepository(tx).CreateOrUpdateLabelsFromItems`), `module/container`, `module/dataset`, `module/injection` (all in `api_types.go` for DTO composition), `internalclient/orchestratorclient`, `middleware`, `consts`, `dto`, `model`.

### module/ratelimiter
- Files: `module.go`, `api_types.go`, `handler.go`, `service.go`
- fx.Module (`module/ratelimiter/module.go:5-8`): Provide `NewService`, `NewHandler`. **No `HandlerService` interface, no adapter, no separate repository.**
- HandlerService: does not exist. `Handler` holds `service *Service` directly (`handler.go:15-17`).
- Handler gin routes:
  - `ListRateLimiters` → `GET /api/v2/rate-limiters` (L34)
  - `ResetRateLimiter` → `DELETE /api/v2/rate-limiters/{bucket}` (L56)
  - `GCRateLimiters`   → `POST /api/v2/rate-limiters/gc` (L81)
- Service (`module/ratelimiter/service.go:25-32`): fields `redis *infra/redis.Gateway`, `db *gorm.DB`. Methods: `List`, `Reset`, `GC`. Backing: token buckets are Redis sets under keys `token_bucket:<bucket>` for `consts.RestartPedestalTokenBucket`, `consts.BuildContainerTokenBucket`, `consts.AlgoExecutionTokenBucket`. GC joins Redis holders with `model.Task` state to release tokens held by terminal tasks.
- Stores: none — direct Redis set ops (`SetMembers`, `SetRemove`, `ScanKeys`, `DeleteKey`).
- Cross-module deps: `consts`, `model` (`model.Task`), `infra/redis`, `httpx`, `dto`.

### module/system
- Files: `module.go`, `api_types.go`, `handler.go`, `handler_service.go`, `handler_test.go`, `repository.go`, `runtime_query.go`, `service.go`, `service_test.go`
- fx.Module (`module/system/module.go:5-13`): Provide `NewRepository`, `newRuntimeQuerySource`, `NewService`, `AsHandlerService`, `NewHandler`.
- HandlerService (`module/system/handler_service.go:10-25`):
  - `GetHealth(ctx) (*HealthCheckResp, error)`
  - `GetMetrics(ctx) (*MonitoringMetricsResp, error)`
  - `GetSystemInfo(ctx) (*SystemInfo, error)`
  - `ListNamespaceLocks(ctx) (*ListNamespaceLockResp, error)`
  - `ListQueuedTasks(ctx) (*QueuedTasksResp, error)`
  - `GetAuditLog(ctx, int) (*AuditLogDetailResp, error)`
  - `ListAuditLogs(ctx, *ListAuditLogReq) (*dto.ListResp[AuditLogResp], error)`
  - `GetConfig(ctx, int) (*ConfigDetailResp, error)`
  - `ListConfigs(ctx, *ListConfigReq) (*dto.ListResp[ConfigResp], error)`
  - `RollbackConfigValue(ctx, *RollbackConfigReq, configID, userID int, ip, ua string) error`
  - `RollbackConfigMetadata(ctx, *RollbackConfigReq, configID, userID int, ip, ua string) (*ConfigResp, error)`
  - `UpdateConfigValue(ctx, *UpdateConfigValueReq, configID, userID int, ip, ua string) error`
  - `UpdateConfigMetadata(ctx, *UpdateConfigMetadataReq, configID, userID int, ip, ua string) (*ConfigResp, error)`
  - `ListConfigHistories(ctx, *ListConfigHistoryReq, configID int) (*dto.ListResp[ConfigHistoryResp], error)`
- Handler gin routes (paths prefixed `/system/...`, no `/api/v2`):
  - `GetHealth`     → `GET /system/health` (L34)
  - `GetMetrics`    → `POST /system/monitor/metrics` (L60)
  - `GetSystemInfo` → `GET /system/monitor/info` (L89)
  - `ListNamespaceLocks` → `GET /system/monitor/namespaces/locks` (L112)
  - `ListQueuedTasks`    → `POST /system/monitor/tasks/queue` (L134)
  - `GetAuditLog`   → `GET /system/audit/{id}` (L158)
  - `ListAuditLogs` → `GET /system/audit` (L194)
  - `GetConfig`     → `GET /system/configs/{config_id}` (L229)
  - `ListConfigs`   → `GET /system/configs` (L263)
  - `RollbackConfigValue`    → `POST /system/configs/{config_id}/value/rollback` (L299)
  - `RollbackConfigMetadata` → `POST /system/configs/{config_id}/metadata/rollback` (L343)
  - `UpdateConfigValue`      → `PATCH /system/configs/{config_id}` (L386)
  - `UpdateConfigMetadata`   → `PUT /system/configs/{config_id}/metadata` (L430)
  - `ListConfigHistories`    → `GET /system/configs/{config_id}/histories` (L477)
- Service (`module/system/service.go:50-79`): built via `serviceParams fx.In` — fields `repo *Repository`, `buildkit *buildkit.Gateway`, `etcd *etcd.Gateway`, `k8s *k8s.Gateway`, `redis *redis.Gateway`, `runtimeQuery runtimeQuerySource`. Public methods match HandlerService; health checks hit each gateway; config updates write `model.ConfigHistory` and push to etcd via `configUpdateContext`.
- Repository (`module/system/repository.go`): on `model.AuditLog`, `model.DynamicConfig`, `model.ConfigHistory`; methods `getAuditLogByID`, `listAuditLogs`, `getConfigByID`, `listConfigs`, `updateConfig`, `getConfigHistory`, `createConfigHistory`, `listConfigHistories`, `listConfigHistoriesByConfigID` — all unexported.
- Notable: `runtime_query.go` — `runtimeQuerySource` abstraction. Local path uses `systemmetric.Service` (`ListNamespaceLocks`, `ListQueuedTasks`). Remote path uses `internalclient/runtimeclient.Client`. `RemoteRuntimeQueryOption()` for dedicated `system-service` app.
- Cross-module deps: `module/systemmetric` (runtime_query), `module/task` (`task.QueuedTasksResp`), `infra/buildkit`, `infra/etcd`, `infra/k8s`, `infra/redis`, `service/common`, `utils`, `config`, `internalclient/runtimeclient`, `middleware`, `consts`, `dto`, `model`.

### module/systemmetric
- Files: `module.go`, `api_types.go`, `handler.go`, `handler_service.go`, `collector.go`, `repository.go`, `service.go`
- fx.Module (`module/systemmetric/module.go:5-13`): Provide `NewRepository`, `NewService`, `AsHandlerService`, `NewHandler`; Invoke `RegisterMetricsCollector`.
- HandlerService (`module/systemmetric/handler_service.go:6-9`):
  - `GetSystemMetrics(ctx) (*SystemMetricsResp, error)`
  - `GetSystemMetricsHistory(ctx) (*SystemMetricsHistoryResp, error)`
- Handler gin routes (`module/systemmetric/handler.go`):
  - `GetSystemMetrics` → `GET /api/v2/system/metrics` (L33)
  - `GetSystemMetricsHistory` → `GET /api/v2/system/metrics/history` (L57)
- Service (`module/systemmetric/service.go:22-29`): fields `repo *Repository` (currently unused — repo is effectively a placeholder), `redis *redisinfra.Gateway`. Exported methods: `GetSystemMetrics` (CPU/mem/disk via `gopsutil`), `GetSystemMetricsHistory` (24 h via Redis ZSet `system:metrics:cpu`/`system:metrics:memory`), `StoreSystemMetrics` (ZAdd + ZRemRangeByScore pruning), `ListNamespaceLocks` (Redis set `consts.NamespacesKey`, hash fields), `ListQueuedTasks` (via `redis.ListReadyTasks` + `redis.ListDelayedTasks`, decoded as `dto.UnifiedTask` → `task.TaskResp`).
- Repository (`module/systemmetric/repository.go`): **empty shell** — only `NewRepository(db *gorm.DB) *Repository` with no methods, entity `None`.
- Stores: n/a — direct `redisinfra.Gateway` usage. Collector writes metrics each minute (`collector.go:11-44`) via `fx.Hook` ticker.
- Cross-module deps: `module/task` (`task.QueuedTasksResp`, `task.TaskResp`), `infra/redis`, `consts`, `dto`.
- `collector.go`: `RegisterMetricsCollector` is the single `fx.Invoke` in the module — spawns a 1-min ticker goroutine on `OnStart` that calls `service.StoreSystemMetrics`, canceled on `OnStop`.

### module/docs
- Files: `swagger_models.go`
- fx.Module: **none**. No `Module` var. Package is documentation-only.
- HandlerService: none.
- Handler: single standalone function `SwaggerModelsDoc(c *gin.Context) {}` that holds Swagger `@Success` model annotations so generated OpenAPI includes `dto.TraceStreamEvent`, `group.GroupStreamEvent`, `dto.DatapackInfo/Result`, `dto.ExecutionInfo/Result`, `dto.InfoPayloadTemplate`, `dto.JobMessage`, and all `consts.*` enums. `@Router /api/_docs/models [get]` — file comment explicitly says this route is **never registered**.
- Cross-module deps: `module/group` (for `GroupStreamEvent` type alias).
- Notable: This is the only module in the slice with no DI wiring and no runtime behavior.

---

## 2. Cross-module dependency edges (deduped)

Outgoing imports from each module in this slice:

- `module/auth` → `module/user` (resource-info DTOs, `NewUserContainerInfo/NewUserDatasetInfo/NewUserProjectInfo`), `infra/redis` (TokenStore), `middleware` (`middleware.TokenVerifier` adapter + `GetCurrentUserID` in handler)
- `module/rbac` → *(leaf — only `consts`, `dto`, `model`, `httpx`)*
- `module/user` → `module/rbac` (`NewRoleResp`, `NewPermissionResp` in DTO hydration)
- `module/team` → `module/project` (type aliases `TeamProjectListReq = project.ListProjectReq`, `TeamProjectItem = project.ProjectResp`; `project.NewProjectResp` in repository), `internalclient/resourceclient` (remote project reader), `middleware` (handler auth helpers)
- `module/project` → `module/container`, `module/dataset`, `module/injection` (DTO aliases/composition in `api_types.go`), `module/label` (`label.NewRepository(tx).CreateOrUpdateLabelsFromItems`), `internalclient/orchestratorclient` (remote statistics source), `middleware`
- `module/ratelimiter` → `infra/redis`, `model` (`model.Task`), `consts`; no other module imports
- `module/system` → `module/systemmetric` (local runtimeQuery path), `module/task` (`task.QueuedTasksResp`), `internalclient/runtimeclient` (remote runtimeQuery), `infra/{buildkit,etcd,k8s,redis}`, `service/common`, `config`, `utils`, `middleware`
- `module/systemmetric` → `module/task` (response types), `infra/redis`; no other module imports
- `module/docs` → `module/group` (type alias for swagger)

Reverse (who consumes this slice elsewhere — spot-check):
- `app/iam/options.go:32` wires `middleware.NewService` and builds auth/rbac/user/team/project locally.
- `app/gateway/options.go:57-80` decorates `auth.HandlerService`, `user.HandlerService`, `rbac.HandlerService`, and `middleware.Service` with remote-aware wrappers backed by `iamclient.Client`.
- `interface/grpc/iam/service.go` consumes `middleware.Service` + the four HandlerServices above for gRPC fan-out.

---

## 3. HandlerService contracts — consolidated local API surface

### auth.HandlerService — `module/auth/handler_service.go:10`
```
Login(ctx, *LoginReq) (*LoginResp, error)
Register(ctx, *RegisterReq) (*UserInfo, error)
RefreshToken(ctx, *TokenRefreshReq) (*TokenRefreshResp, error)
Logout(ctx, *utils.Claims) error
ChangePassword(ctx, *ChangePasswordReq, int) error
GetProfile(ctx, int) (*UserProfileResp, error)
CreateAPIKey(ctx, int, *CreateAPIKeyReq) (*APIKeyWithSecretResp, error)
ListAPIKeys(ctx, int, *ListAPIKeyReq) (*ListAPIKeyResp, error)
GetAPIKey(ctx, int, int) (*APIKeyInfo, error)
DeleteAPIKey(ctx, int, int) error
DisableAPIKey(ctx, int, int) error
EnableAPIKey(ctx, int, int) error
RevokeAPIKey(ctx, int, int) error
RotateAPIKey(ctx, int, int) (*APIKeyWithSecretResp, error)
ExchangeAPIKeyToken(ctx, *APIKeyTokenReq, string, string) (*APIKeyTokenResp, error)
```

### rbac.HandlerService — `module/rbac/handler_service.go:10`
```
CreateRole(ctx, *CreateRoleReq) (*RoleResp, error)
DeleteRole(ctx, int) error
GetRole(ctx, int) (*RoleDetailResp, error)
ListRoles(ctx, *ListRoleReq) (*dto.ListResp[RoleResp], error)
UpdateRole(ctx, *UpdateRoleReq, int) (*RoleResp, error)
AssignRolePermissions(ctx, []int, int) error
RemoveRolePermissions(ctx, []int, int) error
ListUsersFromRole(ctx, int) ([]UserListItem, error)
GetPermission(ctx, int) (*PermissionDetailResp, error)
ListPermissions(ctx, *ListPermissionReq) (*dto.ListResp[PermissionResp], error)
ListRolesFromPermission(ctx, int) ([]RoleResp, error)
GetResource(ctx, int) (*ResourceResp, error)
ListResources(ctx, *ListResourceReq) (*dto.ListResp[ResourceResp], error)
ListResourcePermissions(ctx, int) ([]PermissionResp, error)
```

### user.HandlerService — `module/user/handler_service.go:10`
```
CreateUser(ctx, *CreateUserReq) (*UserResp, error)
DeleteUser(ctx, int) error
GetUserDetail(ctx, int) (*UserDetailResp, error)
ListUsers(ctx, *ListUserReq) (*dto.ListResp[UserResp], error)
UpdateUser(ctx, *UpdateUserReq, int) (*UserResp, error)
AssignRole(ctx, int, int) error
RemoveRole(ctx, int, int) error
AssignPermissions(ctx, *AssignUserPermissionReq, int) error
RemovePermissions(ctx, *RemoveUserPermissionReq, int) error
AssignContainer(ctx, int, int, int) error
RemoveContainer(ctx, int, int) error
AssignDataset(ctx, int, int, int) error
RemoveDataset(ctx, int, int) error
AssignProject(ctx, int, int, int) error
RemoveProject(ctx, int, int) error
```

### team.HandlerService — `module/team/handler_service.go:10`
```
CreateTeam(ctx, *CreateTeamReq, int) (*TeamResp, error)
DeleteTeam(ctx, int) error
GetTeamDetail(ctx, int) (*TeamDetailResp, error)
ListTeams(ctx, *ListTeamReq, int, bool) (*dto.ListResp[TeamResp], error)
UpdateTeam(ctx, *UpdateTeamReq, int) (*TeamResp, error)
ListTeamProjects(ctx, *TeamProjectListReq, int) (*dto.ListResp[TeamProjectItem], error)
AddMember(ctx, *AddTeamMemberReq, int) error
RemoveMember(ctx, int, int, int) error
UpdateMemberRole(ctx, *UpdateTeamMemberRoleReq, int, int, int) error
ListMembers(ctx, *ListTeamMemberReq, int) (*dto.ListResp[TeamMemberResp], error)
```

### project.HandlerService — `module/project/handler_service.go:10`
```
CreateProject(ctx, *CreateProjectReq, int) (*ProjectResp, error)
DeleteProject(ctx, int) error
GetProjectDetail(ctx, int) (*ProjectDetailResp, error)
ListProjects(ctx, *ListProjectReq) (*dto.ListResp[ProjectResp], error)
UpdateProject(ctx, *UpdateProjectReq, int) (*ProjectResp, error)
ManageProjectLabels(ctx, *ManageProjectLabelReq, int) (*ProjectResp, error)
```

### system.HandlerService — `module/system/handler_service.go:10`
```
GetHealth(ctx) (*HealthCheckResp, error)
GetMetrics(ctx) (*MonitoringMetricsResp, error)
GetSystemInfo(ctx) (*SystemInfo, error)
ListNamespaceLocks(ctx) (*ListNamespaceLockResp, error)
ListQueuedTasks(ctx) (*QueuedTasksResp, error)
GetAuditLog(ctx, int) (*AuditLogDetailResp, error)
ListAuditLogs(ctx, *ListAuditLogReq) (*dto.ListResp[AuditLogResp], error)
GetConfig(ctx, int) (*ConfigDetailResp, error)
ListConfigs(ctx, *ListConfigReq) (*dto.ListResp[ConfigResp], error)
RollbackConfigValue(ctx, *RollbackConfigReq, int, int, string, string) error
RollbackConfigMetadata(ctx, *RollbackConfigReq, int, int, string, string) (*ConfigResp, error)
UpdateConfigValue(ctx, *UpdateConfigValueReq, int, int, string, string) error
UpdateConfigMetadata(ctx, *UpdateConfigMetadataReq, int, int, string, string) (*ConfigResp, error)
ListConfigHistories(ctx, *ListConfigHistoryReq, int) (*dto.ListResp[ConfigHistoryResp], error)
```

### systemmetric.HandlerService — `module/systemmetric/handler_service.go:6`
```
GetSystemMetrics(ctx) (*SystemMetricsResp, error)
GetSystemMetricsHistory(ctx) (*SystemMetricsHistoryResp, error)
```

### ratelimiter — **no HandlerService interface** (handler uses `*Service` directly — no gateway decoration path)
### docs — **no HandlerService**, doc-only

---

## 4. Middleware adapter — `module/auth/middleware_adapter.go`

One-liner:
```go
func NewTokenVerifier(service *Service) middleware.TokenVerifier { return service }
```
It adapts `*auth.Service` (which already has `VerifyToken` `module/auth/service.go:172` and `VerifyServiceToken` L191) to the `middleware.TokenVerifier` interface defined at `middleware/deps.go:18-21`.

Wiring chain:
1. `module/auth/module.go:14` provides `middleware.TokenVerifier` from `*auth.Service`.
2. `interface/http/module.go:12` and `app/iam/options.go:32` provide `middleware.Service` via `middleware.NewService(db *gorm.DB, verifier middleware.TokenVerifier)` (`middleware/deps.go:45`).
3. `router.New` consumes `middleware.Service` (`router/router.go:13`) and installs `middleware.InjectService(service)` (`middleware/deps.go:49`) which stashes the service under context key `"middleware.service"`.
4. `middleware.JWTAuth` (`middleware/auth.go:18-66`) calls `serviceFromContext(c)` (`middleware/deps.go:64`) and invokes `service.VerifyToken` → falls through to the auth.Service implementation, which also hits `TokenStore.IsTokenBlacklisted` via Redis (`module/auth/service.go:178-186`).
5. In gateway mode, `app/gateway/options.go:63-68` decorates the resulting `middleware.Service` with a remote-aware wrapper delegating verification to `iamclient.Client`.

Net effect: the middleware package never imports the auth module (no cycle); `auth.Service` becomes the concrete token verifier via fx's interface assertion and the `InjectService` gin middleware.

---

## 5. RBAC truth source

### Runtime permission check
The **runtime checker is not in `module/rbac`** — it lives in `middleware/deps.go:98-265` on `dbBackedMiddlewareService.CheckUserPermission`.

- Input: `dto.CheckPermissionParams{UserID, Action, Scope, ResourceName, ProjectID?, TeamID?, ContainerID?, DatasetID?}` — `dto/permission.go:10`.
- Algorithm (`middleware/deps.go:242-265`):
  1. Look up the single matching `permissions` row by `(action, scope, resource_name)` — `getPermissionByActionAndResource` (L229).
  2. UNION-ALL 5 subqueries against `role_permissions` / `user_permissions`:
     - direct grant: `user_permissions` filtered by optional project/container/dataset IDs (L267)
     - global role: `role_permissions JOIN user_roles` (L294)
     - team role (optional): `role_permissions JOIN user_teams` (L302)
     - project role (optional): `role_permissions JOIN user_projects` (L311)
     - container role (optional): `role_permissions JOIN user_containers` (L320)
     - dataset role (optional): `role_permissions JOIN user_datasets` (L329)
  3. Count rows; presence → true.

### `module/rbac` role
`module/rbac/service.go` exposes **CRUD** over roles, permissions, resources, and the `role_permissions` join — it does **not** run the permission check. It just keeps the catalogue in sync.

### Permission matrix / PermissionRule shape
Out of slice, at `consts/system.go:177-199`:
```go
type PermissionRule struct {
    Resource ResourceName   // e.g. "container"
    Action   ActionName     // "read", "update", "assign", "manage", ...
    Scope    ResourceScope  // "all", "own", "team", ...
}
// ParsePermissionRule splits "container:read:own" into the three parts.
```
Predefined rules live as package-level vars (`PermSystemRead`, `PermRoleGrantAll`, `PermTeamReadAll`, ...) starting at `consts/system.go:204`. These are the single source of truth for which `(resource, action, scope)` triples the system knows about; the DB `permissions` table is expected to mirror this set.

Role names: `consts.RoleSuperAdmin`, `RoleAdmin`, `RoleUser`, `RoleContainer{Admin,Developer,Viewer}`, `RoleDataset*`, `RoleProject{Admin,AlgoDeveloper,DataDeveloper,Viewer}`, `RoleTeam{Admin,Member,Viewer}` (`consts/system.go:150-173`).

So the in-slice module defines **catalog CRUD**; the **check logic** is in `middleware/deps.go`, and the **enumerated matrix** lives in `consts/system.go`.

---

## 6. Surprises / dead code

1. **`module/docs/swagger_models.go` is runtime-dead.** Comment states the route must never be registered. Sole purpose: pull `consts.*` + cross-module DTO types into the Swagger doc graph via `@Success` annotations. No fx wiring exists.
2. **`module/systemmetric/repository.go` is a stub.** Only `NewRepository` with no methods — `Service` never uses the field (`service.go:22` has `repo *Repository` but reads/writes go directly to `redisinfra.Gateway`). Candidate for deletion.
3. **`module/ratelimiter` breaks the module convention.** No `HandlerService` interface, no `AsHandlerService` adapter — the handler holds `*Service` directly. Cannot be remote-wrapped via `fx.Decorate` in gateway mode. Also: no `repository.go` despite reading `model.Task` state — queries are inline in `service.go:166`.
4. **`module/auth/service.go:172 VerifyToken` and `:191 VerifyServiceToken`** are on the concrete `*Service` but **not** in the `HandlerService` interface. They are accessed exclusively through the `middleware.TokenVerifier` interface (`middleware_adapter.go`). Gateway's `remoteAwareAuthService` (auth decorator) therefore cannot intercept them — token verification delegation happens on the separate `middleware.Service` decorator in `app/gateway/options.go:63-68`.
5. **`module/team/service.go:175-206`** exports `IsUserInTeam`, `IsUserTeamAdmin`, `IsTeamPublic` that look designed to feed `middleware.permissionChecker`, but `middleware/deps.go:114-145` reimplements those checks against the database directly. The team.Service versions are effectively unused by the middleware pipeline.
6. **Three abstraction patterns for local/remote split** appear within this slice — `team.projectReader`, `project.projectStatisticsSource`, `system.runtimeQuerySource` — each with its own `Remote*Option()` `fx.Decorate` helper. Consider unifying.
7. **`module/auth/service.go:526` `getAllUserResourceRoles`** duplicates the repository’s per-type list methods (`ListContainerRoles`/`ListDatasetRoles`/`ListProjectRoles`), and the conversion layer reaches into `module/user` constructors (`user.NewUserContainerInfo` etc.). The same conversion exists in `module/user/service.go:313 buildUserResourceRoles` — two slightly different copies of the same hydration.
8. **`module/rbac/repository.go:312 listPermissionsByIDs`** is near-identical to `module/user/repository.go:377 listPermissionsByIDs`. Same query, two copies.
9. **`module/rbac/handler.go`** `DeleteRole` returns `200` but the rest of the slice uses `204` (`module/auth/handler.go:369`). Inconsistent status codes for no-content operations.
10. **`module/system`** handler paths are rooted at `/system/...` rather than the `/api/v2/...` used everywhere else in the slice (e.g. `/system/health`, `/system/configs/...`). Likely intentional for infra health endpoints but worth flagging.
