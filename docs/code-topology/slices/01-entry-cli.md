# 01 — Entry points & CLI dispatch (AegisLab `src/`)

Source-of-truth pass: only `.go` / `.toml` were read. Doc files ignored. All file paths are absolute under `/home/ddq/AoyangSpace/aegis/AegisLab/src/`.

---

## 1. Cobra subcommands of the backend binary `rcabench`

Root `cobra.Command{Use:"rcabench"}` is built in `main.go:73-79`; default `Run` only logs `"Please specify a mode..."` (`main.go:76-78`). Persistent flags `--port` / `-p` and `--conf` / `-c` are bound to viper (`main.go:81-89`). `config.Init(viper.GetString("conf"))` runs unconditionally before `rootCmd.Execute()` (`main.go:91`). Three subcommands are wired via `rootCmd.AddCommand(producerCmd, consumerCmd, bothCmd)` at `main.go:205`.

| Subcommand | Startup call sequence (top → bottom) | main.go lines |
|------------|--------------------------------------|---------------|
| `producer` | `database.InitDB` → `initialization.InitializeProducer(ctx)` → `utils.InitValidator` → `client.InitTraceProvider` → `initChaosExperiment` (which calls `k8s.GetK8sRestConfig` then `chaosCli.InitWithConfig`) → `router.New()` → `engine.Run(":"+port)` | 97-115 |
| `consumer` | `consts.InitialTime = utils.TimePtr(time.Now())` → `consts.AppID = utils.GenerateULID(...)` → `k8slogger.SetLogger(stdr.New(...))` → `initChaosExperiment` → `go k8s.GetK8sController().Initialize(ctx, cancel, consumer.NewHandler())` → `database.InitDB` → `initialization.InitializeConsumer(ctx)` → `initialization.InitConcurrencyLock(ctx)` → `client.InitTraceProvider` → `runRateLimiterStartupGC(ctx)` (goroutine wrapping `producer.GCRateLimiters`, see `main.go:213-223`) → goroutine: `logreceiver.NewOTLPLogReceiver(port, 0).Start(ctx)` (port from `config.GetInt("otlp_receiver.port")`, fallback `logreceiver.DefaultPort`) → `go consumer.StartScheduler(ctx)` → `consumer.ConsumeTasks(ctx)` (blocking) | 118-155 |
| `both` | identical to `consumer` startup PLUS `initialization.InitializeProducer(ctx)`, `utils.InitValidator`, then both `go consumer.StartScheduler(ctx)` and `go consumer.ConsumeTasks(ctx)` (note: `ConsumeTasks` is async here, blocking in `consumer`), and finally `router.New()` + `engine.Run` | 158-203 |

Other startup-time hooks in `main.go`: package `init()` configures logrus (`main.go:50-63`); `runRateLimiterStartupGC` calls `producer.GCRateLimiters` and logs released token count (`main.go:213-223`).

---

## 2. HTTP route table

Entry: `router.New()` in `router/router.go:12-41` — builds `gin.Default()` (so default `Logger` + `Recovery`), then **global** middleware chain `middleware.GroupID()`, `middleware.SSEPath()`, `cors.New(corsConfig)` (allow-all origins/credentials), `middleware.TracerMiddleware()` (`router.go:24-29`). Then `SetupSystemRoutes(router)`, `SetupV2Routes(router)`, and Swagger handler `router.GET("/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))` (`router.go:32-38`).

**Per-route middlewares** (referenced under `aegis/middleware`): `JWTAuth`, `Require<*>` permission gates, `RequireSystemAdmin()`, `StartCleanupRoutine()` invoked once at top of `SetupV2Routes` (`v2.go:113`).

### 2a. `router/system.go` — `/system/*`

| Method | Path | Handler | Per-route middleware |
|--------|------|---------|----------------------|
| GET    | `/system/audit`                          | `system.ListAuditLogs`         | `JWTAuth, RequireAuditRead` |
| GET    | `/system/audit/:id`                      | `system.GetAuditLog`           | `JWTAuth, RequireAuditRead` |
| GET    | `/system/configs`                        | `system.ListConfigs`           | `JWTAuth, RequireConfigurationRead` |
| GET    | `/system/configs/:config_id`             | `system.GetConfig`             | `JWTAuth, RequireConfigurationRead` |
| GET    | `/system/configs/:config_id/histories`   | `system.ListConfigHistories`   | `JWTAuth, RequireConfigurationRead` |
| PATCH  | `/system/configs/:config_id`             | `system.UpdateConfigValue`     | `JWTAuth, RequireConfigurationUpdate` |
| POST   | `/system/configs/:config_id/value/rollback`    | `system.RollbackConfigValue`     | `JWTAuth, RequireConfigurationUpdate` |
| PUT    | `/system/configs/:config_id/metadata`          | `system.UpdateConfigMetadata`    | `JWTAuth, RequireConfigurationConfigure` |
| POST   | `/system/configs/:config_id/metadata/rollback` | `system.RollbackConfigMetadata`  | `JWTAuth, RequireConfigurationConfigure` |
| GET    | `/system/health`                                | `system.GetHealth`               | (none — public) |
| POST   | `/system/monitor/metrics`                       | `system.GetMetrics`              | `JWTAuth, RequireSystemRead` |
| GET    | `/system/monitor/info`                          | `system.GetSystemInfo`           | `JWTAuth, RequireSystemRead` |
| GET    | `/system/monitor/namespaces/locks`              | `system.ListNamespaceLocks`      | `JWTAuth, RequireSystemRead` |
| GET    | `/system/monitor/tasks/queue`                   | `system.ListQueuedTasks`         | `JWTAuth, RequireSystemRead` |

(All paths defined in `router/system.go:11-49`.)

### 2b. `router/v2.go` — `/api/v2/*`

Group prefix `/api/v2` declared at `v2.go:115`. Lines below cite `router/v2.go`.

**auth** (group `auth.JWTAuth()` only on `authProtected` subgroup) — lines 117-130

| METH | PATH | HANDLER |
|------|------|---------|
| POST | /api/v2/auth/login           | `v2handlers.Login` |
| POST | /api/v2/auth/register        | `v2handlers.Register` |
| POST | /api/v2/auth/refresh         | `v2handlers.RefreshToken` |
| POST | /api/v2/auth/logout          | `v2handlers.Logout` (JWTAuth) |
| POST | /api/v2/auth/change-password | `v2handlers.ChangePassword` (JWTAuth) |
| GET  | /api/v2/auth/profile         | `v2handlers.GetProfile` (JWTAuth) |

**containers** — lines 137-191. All under `JWTAuth`.

| METH | PATH | HANDLER | extra middleware |
|------|------|---------|------------------|
| GET    | /api/v2/containers/:container_id                                  | `GetContainer`                | `RequireContainerRead` |
| GET    | /api/v2/containers                                                | `ListContainers`              | `RequireContainerRead` |
| POST   | /api/v2/containers                                                | `CreateContainer`             | `RequireContainerCreate` |
| POST   | /api/v2/containers/build                                          | `SubmitContainerBuilding`     | `RequireContainerExecute` |
| PATCH  | /api/v2/containers/:container_id                                  | `UpdateContainer`             | `RequireContainerUpdate` |
| PATCH  | /api/v2/containers/:container_id/labels                           | `ManageContainerCustomLabels` | `RequireContainerUpdate` |
| DELETE | /api/v2/containers/:container_id                                  | `DeleteContainer`             | `RequireContainerDelete` |
| GET    | /api/v2/containers/:container_id/versions/:version_id             | `GetContainerVersion`         | `RequireContainerVersionRead` |
| GET    | /api/v2/containers/:container_id/versions                         | `ListContainerVersions`       | `RequireContainerVersionRead` |
| POST   | /api/v2/containers/:container_id/versions                         | `CreateContainerVersion`      | `RequireContainerVersionCreate` |
| POST   | /api/v2/containers/:container_id/versions/:version_id/helm-chart  | `UploadHelmChart`             | `RequireContainerVersionUpload` |
| POST   | /api/v2/containers/:container_id/versions/:version_id/helm-values | `UploadHelmValueFile`         | `RequireContainerVersionUpload` |
| PATCH  | /api/v2/containers/:container_id/versions/:version_id             | `UpdateContainerVersion`      | `RequireContainerVersionUpdate` |
| DELETE | /api/v2/containers/:container_id/versions/:version_id             | `DeleteContainerVersion`      | `RequireContainerVersionDelete` |
| PATCH  | /api/v2/container-versions/:id/image                              | `SetContainerVersionImage`    | `RequireContainerVersionUpdate` (group at 188-191) |

**datasets** — lines 194-232 (all `JWTAuth`)

| METH | PATH | HANDLER | extra |
|------|------|---------|-------|
| GET    | /api/v2/datasets/:dataset_id | `GetDataset` | `RequireDatasetRead` |
| GET    | /api/v2/datasets             | `ListDatasets` | `RequireDatasetRead` |
| POST   | /api/v2/datasets             | `CreateDataset` | `RequireDatasetCreate` |
| PATCH  | /api/v2/datasets/:dataset_id | `UpdateDataset` | `RequireDatasetUpdate` |
| PATCH  | /api/v2/datasets/:dataset_id/labels | `ManageDatasetCustomLabels` | `RequireDatasetUpdate` |
| DELETE | /api/v2/datasets/:dataset_id | `DeleteDataset` | `RequireDatasetDelete` |
| GET    | /api/v2/datasets/:dataset_id/versions | `ListDatasetVersions` | `RequireDatasetVersionRead` |
| GET    | /api/v2/datasets/:dataset_id/versions/:version_id | `GetDatasetVersion` | `RequireDatasetVersionRead` |
| GET    | /api/v2/datasets/:dataset_id/versions/:version_id/download | `DownloadDatasetVersion` | `RequireDatasetVersionRead` |
| POST   | /api/v2/datasets/:dataset_id/versions | `CreateDatasetVersion` | `RequireDatasetVersionCreate` |
| PATCH  | /api/v2/datasets/:dataset_id/versions/:version_id | `UpdateDatasetVersion` | `RequireDatasetVersionUpdate` |
| PATCH  | /api/v2/datasets/:dataset_id/versions/:version_id/injections | `ManageDatasetVersionInjections` | `RequireDatasetVersionUpdate` |
| DELETE | /api/v2/datasets/:dataset_id/versions/:version_id | `DeleteDatasetVersion` | `RequireDatasetVersionDelete` |

**projects** — lines 235-287 (all `JWTAuth`). Sub-resources: `injections`, `executions`.

| METH | PATH | HANDLER | extra |
|------|------|---------|-------|
| GET  | /api/v2/projects/:project_id | `GetProjectDetail` | `RequireProjectRead` |
| GET  | /api/v2/projects             | `ListProjects` | `RequireProjectRead` |
| POST | /api/v2/projects             | `CreateProject` | `RequireProjectCreate` |
| PATCH | /api/v2/projects/:project_id | `UpdateProject` | `RequireProjectUpdate` |
| PATCH | /api/v2/projects/:project_id/labels | `ManageProjectCustomLabels` | `RequireProjectUpdate` |
| DELETE | /api/v2/projects/:project_id | `DeleteProject` | `RequireProjectDelete` |
| GET  | /api/v2/projects/:project_id/injections/analysis/no-issues | `ListFaultInjectionNoIssues` | `RequireProjectRead` |
| GET  | /api/v2/projects/:project_id/injections/analysis/with-issues | `ListFaultInjectionWithIssues` | `RequireProjectRead` |
| GET  | /api/v2/projects/:project_id/injections | `ListProjectInjections` | `RequireProjectRead` |
| POST | /api/v2/projects/:project_id/injections/search | `SearchInjections` | `RequireProjectRead` |
| POST | /api/v2/projects/:project_id/injections/inject | `SubmitProjectFaultInjection` | `RequireProjectInjectionExecute` |
| POST | /api/v2/projects/:project_id/injections/build | `SubmitProjectDatapackBuilding` | `RequireProjectInjectionExecute` |
| GET  | /api/v2/projects/:project_id/executions | `ListProjectExecutions` | `RequireProjectRead` |
| POST | /api/v2/projects/:project_id/executions/execute | `SubmitAlgorithmExecution` | `RequireProjectExecutionExecute` |

**teams** — lines 290-318 (`JWTAuth`)

| METH | PATH | HANDLER | extra |
|------|------|---------|-------|
| POST   | /api/v2/teams                              | `CreateTeam` | – |
| GET    | /api/v2/teams                              | `ListTeams` | – |
| PATCH  | /api/v2/teams/:team_id                     | `UpdateTeam` | `RequireTeamAdminAccess` |
| DELETE | /api/v2/teams/:team_id                     | `DeleteTeam` | `RequireTeamAdminAccess` |
| POST   | /api/v2/teams/:team_id/members             | `AddTeamMember` | `RequireTeamAdminAccess` |
| DELETE | /api/v2/teams/:team_id/members/:user_id    | `RemoveTeamMember` | `RequireTeamAdminAccess` |
| PATCH  | /api/v2/teams/:team_id/members/:user_id/role | `UpdateTeamMemberRole` | `RequireTeamAdminAccess` |
| GET    | /api/v2/teams/:team_id                     | `GetTeamDetail` | `RequireTeamMemberAccess` |
| GET    | /api/v2/teams/:team_id/members             | `ListTeamMembers` | `RequireTeamMemberAccess` |
| GET    | /api/v2/teams/:team_id/projects            | `ListTeamProjects` | `RequireTeamMemberAccess` |

**labels** — lines 321-339 (`JWTAuth`); `RequireLabel<Read|Create|Update|Delete>` per op. Endpoints: `GET /labels/:label_id` `GetLabelDetail`, `GET /labels` `ListLabels`, `POST /labels` `CreateLabel`, `PATCH /labels/:label_id` `UpdateLabel`, `DELETE /labels/:label_id` `DeleteLabel`, `POST /labels/batch-delete` `BatchDeleteLabels`.

**users** — lines 342-394 (`JWTAuth`). Reads: `ListUsersV2`, `GetUserDetailV2` (`RequireUserRead`+`RequireAdminOrUserOwnership`). Mutations: `CreateUser`, `UpdateUser`, `DeleteUser` (`RequireUserCreate/Update/Delete`). Assignments (`RequireUserAssign`):

| METH | PATH | HANDLER |
|------|------|---------|
| POST   | /api/v2/users/:user_id/roles/:role_id     | `AssignUserRole` |
| DELETE | /api/v2/users/:user_id/roles/:role_id     | `RemoveGlobalRole` |
| POST   | /api/v2/users/:user_id/projects/:project_id/roles/:role_id | `AssignUserProject` |
| DELETE | /api/v2/users/:user_id/projects/:project_id | `RemoveUserProject` |
| POST   | /api/v2/users/:user_id/permissions/assign | `AssignUserPermission` |
| POST   | /api/v2/users/:user_id/permissions/remove | `RemoveUserPermission` |
| POST   | /api/v2/users/:user_id/containers/:container_id/roles/:role_id | `AssignUserContainer` |
| DELETE | /api/v2/users/:user_id/containers/:container_id | `RemoveUserContainer` |
| POST   | /api/v2/users/:user_id/datasets/:dataset_id/roles/:role_id | `AssignUserDataset` |
| DELETE | /api/v2/users/:user_id/datasets/:dataset_id | `RemoveUserDataset` |

**roles / permissions / resources** — lines 401-462 (`JWTAuth` + per-op `RequireRole*`/`RequirePermissionRead`). Includes `AssignRolePermission`, `RemovePermissionsFromRole`, `ListUsersFromRole`, `ListRoles`, `GetRole`, `CreateRole`, `UpdateRole`, `DeleteRole`, `ListRolesFromPermission`, `ListPermissions`, `GetPermission`, `ListResourcePermissions`, `GetResourceDetail`, `ListResources`.

**tasks** — lines 469-490

| METH | PATH | HANDLER | middleware |
|------|------|---------|------------|
| GET  | /api/v2/tasks                       | `ListTasks` | `JWTAuth, RequireTaskRead` |
| GET  | /api/v2/tasks/:task_id              | `GetTask` | `JWTAuth, RequireTaskRead` |
| POST | /api/v2/tasks/batch-delete          | `BatchDeleteTasks` | `JWTAuth, RequireTaskDelete` |
| POST | /api/v2/tasks/:task_id/expedite     | `ExpediteTask` | `JWTAuth, RequireTaskExecute` |
| GET  | /api/v2/tasks/:task_id/logs/ws      | `GetTaskLogsWS` | (no JWT middleware — auth via WS query param, see comment at v2.go:488) |

**injections** (global) — lines 494-528 (`JWTAuth`)

| METH | PATH | HANDLER | extra |
|------|------|---------|-------|
| GET  | /api/v2/injections                  | `ListInjections` | `RequireSystemAdmin()` |
| POST | /api/v2/injections/search           | `SearchInjections` | `RequireSystemAdmin()` |
| POST | /api/v2/injections/upload           | `UploadDatapack` | – |
| GET  | /api/v2/injections/systems          | `GetSystemMapping` | – |
| POST | /api/v2/injections/translate        | `TranslateFaultSpecs` | – |
| GET  | /api/v2/injections/:id              | `GetInjection` | – |
| GET  | /api/v2/injections/:id/download     | `DownloadDatapack` | – |
| GET  | /api/v2/injections/:id/logs         | `GetInjectionLogs` | – |
| GET  | /api/v2/injections/:id/files        | `ListDatapackFiles` | – |
| GET  | /api/v2/injections/:id/files/download | `DownloadDatapackFile` | – |
| GET  | /api/v2/injections/:id/files/query  | `QueryDatapackFile` | – |
| GET  | /api/v2/injections/metadata         | `GetInjectionMetadata` | – |
| POST | /api/v2/injections/:id/clone        | `CloneInjection` | – |
| PUT  | /api/v2/injections/:id/groundtruth  | `UpdateGroundtruth` | – |
| PATCH| /api/v2/injections/:id/labels       | `ManageInjectionCustomLabels` | – |
| PATCH| /api/v2/injections/labels/batch     | `BatchManageInjectionLabels` | – |
| POST | /api/v2/injections/batch-delete     | `BatchDeleteInjections` | – |

(NOTE: `/translate` and `/metadata` are still active routes — the memory note about them being `410` does not match the code. See "Surprises".)

**executions** (global) — lines 532-550 (`JWTAuth`). `ListExecutions`, `ListAvaliableExecutionLabels` (`RequireSystemAdmin()`); `GetExecution`, `UploadDetectorResults`, `UploadGranularityResults`, `ManageExecutionCustomLabels`, `BatchDeleteExecutions`.

**traces / groups / notifications / analyzer / evaluations** — lines 553-606 (`JWTAuth`). 

| METH | PATH | HANDLER | comment |
|------|------|---------|---------|
| GET  | /api/v2/traces                                | `ListTraces`              | – |
| GET  | /api/v2/traces/:trace_id                      | `GetTrace`                | – |
| GET  | /api/v2/traces/:trace_id/stream               | `GetTraceStream`          | **SSE** |
| GET  | /api/v2/groups/:group_id/stats                | `GetAlgorithmMetrics`     | – |
| GET  | /api/v2/groups/:group_id/stream               | `GetGroupStream`          | **SSE** |
| GET  | /api/v2/notifications/stream                  | `GetNotificationStream`   | **SSE** |
| GET  | /api/v2/evaluations                           | `ListEvaluations`         | – |
| GET  | /api/v2/evaluations/:id                       | `GetEvaluation`           | – |
| DELETE | /api/v2/evaluations/:id                     | `DeleteEvaluation`        | – |
| POST | /api/v2/evaluations/datasets                  | `ListDatasetEvaluationResults` | `RequireDatasetRead` |
| POST | /api/v2/evaluations/datapacks                 | `ListDatapackEvaluationResults`| `RequireDatasetRead` |

`analyzer := v2.Group("/analyzer", JWTAuth())` is declared then `_ = analyzer` (`v2.go:582-583`) — **dead group** (see Surprises).

**sdk / metrics / pedestal / systems / system-metrics / rate-limiters** — lines 612-697 (`JWTAuth`)

| METH | PATH | HANDLER | extra |
|------|------|---------|-------|
| GET  | /api/v2/sdk/evaluations               | `ListSDKEvaluations` | – |
| GET  | /api/v2/sdk/evaluations/experiments   | `ListSDKExperiments` | – |
| GET  | /api/v2/sdk/evaluations/:id           | `GetSDKEvaluation` | – |
| GET  | /api/v2/sdk/datasets                  | `ListSDKDatasetSamples` | – |
| GET  | /api/v2/metrics/injections            | `GetInjectionMetrics` | – |
| GET  | /api/v2/metrics/executions            | `GetExecutionMetrics` | – |
| GET  | /api/v2/metrics/algorithms            | `GetAlgorithmMetrics` | – |
| GET  | /api/v2/pedestal/helm/:container_version_id        | `GetPedestalHelmConfig` | – |
| POST | /api/v2/pedestal/helm/:container_version_id/verify | `VerifyPedestalHelmConfig` | – |
| PUT  | /api/v2/pedestal/helm/:container_version_id        | `UpsertPedestalHelmConfig` | `RequireContainerVersionUpload` |
| GET  | /api/v2/systems                       | `ListChaosSystemsHandler` | – |
| POST | /api/v2/systems                       | `CreateChaosSystemHandler` | – |
| GET  | /api/v2/systems/:id                   | `GetChaosSystemHandler` | – |
| PUT  | /api/v2/systems/:id                   | `UpdateChaosSystemHandler` | – |
| DELETE | /api/v2/systems/:id                 | `DeleteChaosSystemHandler` | – |
| POST | /api/v2/systems/:id/metadata          | `UpsertChaosSystemMetadataHandler` | – |
| GET  | /api/v2/systems/:id/metadata          | `ListChaosSystemMetadataHandler` | – |
| GET  | /api/v2/system/metrics                | `GetSystemMetrics` | – |
| GET  | /api/v2/system/metrics/history        | `GetSystemMetricsHistory` | – |
| GET  | /api/v2/rate-limiters                 | `ListRateLimiters` | – |
| DELETE | /api/v2/rate-limiters/:bucket       | `ResetRateLimiter` | `RequireSystemAdmin()` |
| POST | /api/v2/rate-limiters/gc              | `GCRateLimiters` | `RequireSystemAdmin()` |

(Plus `GET /docs/*any` for Swagger, registered in `router.go:38`.)

---

## 3. aegisctl CLI

Entry: `cmd/aegisctl/main.go:8` calls `cmd.Execute()` (`cmd/root.go:181-185`). `rootCmd` (`cmd/root.go:29-150`) sets `PersistentPreRunE` that resolves config via `config.LoadConfig`, then `--server / --token / --project / --output / --request-timeout` flags fall back to env (`AEGIS_SERVER`, `AEGIS_TOKEN`, `AEGIS_PROJECT`, `AEGIS_OUTPUT`, `AEGIS_TIMEOUT`) then to context preferences. Wire `output.Quiet = flagQuiet` (`cmd/root.go:146`).

Subcommands registered explicitly in `root.go:162-178`: `authCmd, contextCmd, projectCmd, containerCmd, injectCmd, executeCmd, taskCmd, traceCmd, datasetCmd, evalCmd, waitCmd, statusCmd, completionCmd, pedestalCmd`. Two more attach themselves directly via `rootCmd.AddCommand(...)` inside their own `init()`: `clusterCmd` (`cmd/cluster.go:70`) and `rateLimiterCmd` (`cmd/rate_limiter.go:162`).

Helpers `newClient()` / `newResolver()` in `cmd/project.go:32-38` return `client.NewClient(flagServer, flagToken, ...)` and `client.NewResolver`.

| Command (path) | file:line | Behaviour (from function body) | Backend endpoint(s) |
|----------------|-----------|--------------------------------|---------------------|
| `auth login` | cmd/auth.go:26-83 | requires `--server/--username/--password`, calls `client.Login`, stores token+expiry in `cfg.Contexts[ctxName]`, persists via `config.SaveConfig` | `POST /api/v2/auth/login` |
| `auth status` | cmd/auth.go:87-141 | reads current context; if token present, calls `client.GetProfile` to verify | `GET /api/v2/auth/profile` |
| `auth token` | cmd/auth.go:147-188 | `--set`: writes token into context; otherwise prints truncated current token | (none) |
| `context set` | cmd/context.go:23-56 | upserts a context entry (server/default-project) | (none) |
| `context use <name>` | cmd/context.go:60-75 | `config.SetCurrentContext` then save | (none) |
| `context list` | cmd/context.go:79-107 | lists `cfg.Contexts` | (none) |
| `project list` | cmd/project.go:51-75 | paginated GET, table/JSON | `GET /api/v2/projects?page=&size=` |
| `project get <name>` | cmd/project.go:79-110 | resolver → ID; GET detail | `GET /api/v2/projects/:id` (+ `GET /api/v2/projects?page=1&size=100` via resolver) |
| `project create` | cmd/project.go:117-145 | POST `{name, description}` | `POST /api/v2/projects` |
| `container list` | cmd/container.go:65-96 | GET `/api/v2/containers?page=1&size=100`, optional `&type=<int>` (algorithm/benchmark/pedestal) | `GET /api/v2/containers` |
| `container get <name>` | cmd/container.go:100-144 | resolver → id, GET detail; computes default version locally | `GET /api/v2/containers/:id` |
| `container versions <name>` | cmd/container.go:148-181 | resolver → id, list versions | `GET /api/v2/containers/:id/versions` |
| `container build <name>` | cmd/container.go:376-403 | POST `{name, version?}` | `POST /api/v2/containers/build` |
| `container version set-image` | cmd/container.go:215-276 | parse `<ref>` → `{registry,namespace,repository,tag}`; fetch current via `fetchContainerVersionByID` (scans all containers + versions); `--dry-run` prints diff; otherwise PATCH | `GET /api/v2/containers?page=1&size=1000`, `GET /api/v2/containers/:id/versions?page=1&size=1000`, `PATCH /api/v2/container-versions/:id/image` |
| `container version list-versions` | cmd/container.go:282-296 + 337-370 | resolver → id; list versions w/ IMAGE column | `GET /api/v2/containers/:id/versions` |
| `dataset list` | cmd/dataset.go:46-69 | GET datasets | `GET /api/v2/datasets?page=1&size=100` |
| `dataset get <name>` | cmd/dataset.go:73-104 | resolver → id; GET detail | `GET /api/v2/datasets/:id` |
| `dataset versions <name>` | cmd/dataset.go:108-138 | list dataset versions | `GET /api/v2/datasets/:id/versions` |
| `inject submit` | cmd/inject.go:172-242 | reads YAML spec, resolves project ID, POSTs raw FaultSpec envelope. With `--wait`, opens SSE on trace and runs `runInjectSubmitWait → runInjectWait` | `POST /api/v2/projects/:pid/injections/inject`; (wait) `GET /api/v2/traces/:trace_id/stream` (SSE); (post-wait) `GET /api/v2/injections?page=1&size=100` to resolve injection name → id |
| `inject list` | cmd/inject.go:340-401 | resolves project id; paginated GET; filter flags | `GET /api/v2/projects/:pid/injections?page=&size=&state=&fault_type=&labels=` |
| `inject get <name>` | cmd/inject.go:405-425 | resolver → injection id; GET detail | `GET /api/v2/injections/:id` |
| `inject search` | cmd/inject.go:434-460 | POST `{name_pattern, labels}` to project search | `POST /api/v2/projects/:pid/injections/search` |
| `inject logs <name>` | cmd/inject.go:464-484 | GET injection logs | `GET /api/v2/injections/:id/logs` |
| `inject files <name>` | cmd/inject.go:488-524 | GET file listing | `GET /api/v2/injections/:id/files` |
| `inject download <name>` | cmd/inject.go:530-581 | RAW `http.NewRequest` (not via `client.Client`), writes to `--output-file` | `GET /api/v2/injections/:id/download` |
| `inject metadata` | cmd/inject.go:587-666 | GET metadata, render fault-type / system tables | `GET /api/v2/injections/metadata[?system=]` |
| `inject describe <type>` | cmd/inject.go:670-751 | GET metadata, look up fault type, print field table + YAML template | `GET /api/v2/injections/metadata` |
| `inject guided` | cmd/inject_guided.go:86-224 | uses `chaos-experiment/pkg/guidedcli` (`LoadConfig`, `MergeConfig`, `Resolve`, `ApplyNextSelection`, `SaveConfig`); persists `~/.aegisctl/inject-guided.yaml`. With `--apply`, calls `submitGuidedApply` which builds a SubmitInjectionReq envelope wrapping the GuidedConfig as a single spec | `POST /api/v2/projects/:pid/injections/inject` (only on `--apply`); otherwise: NO backend call |
| `execute submit` | cmd/execute.go:73-107 | reads YAML, resolves project id, POSTs spec | `POST /api/v2/projects/:pid/executions/execute` |
| `execute list` | cmd/execute.go:116-162 | paginated list | `GET /api/v2/projects/:pid/executions?page=&size=` |
| `execute get <id>` | cmd/execute.go:166-182 | GET by id (numeric, no resolve) | `GET /api/v2/executions/:id` |
| `task list` | cmd/task.go:49-119 | optional `--state/--type/--page/--size`; client-side `--overdue` filter on Pending tasks (compares `execute_time` to `time.Now().Unix()`) | `GET /api/v2/tasks?...` |
| `task expedite <id>` | cmd/task.go:123-150 | POST | `POST /api/v2/tasks/:id/expedite` |
| `task get <id>` | cmd/task.go:185-209 | GET task | `GET /api/v2/tasks/:id` |
| `task logs <id>` | cmd/task.go:215-278 | opens WebSocket via `client.NewWSReader`, follow or 5s timeout | `WS /api/v2/tasks/:id/logs/ws?token=...` |
| `trace list` | cmd/trace.go:49-111 | optional project name → id resolve; GET list | `GET /api/v2/traces?...` (+ `GET /api/v2/projects?page=1&size=100` if resolving) |
| `trace get <id>` | cmd/trace.go:115-167 | GET trace, render header + tasks table | `GET /api/v2/traces/:id` |
| `trace watch <id>` | cmd/trace.go:171-218 | SSE consume; terminates on `event=completed/failed/cancelled` or state=`Completed/Failed/Cancelled/Error` | `GET /api/v2/traces/:id/stream` (SSE) |
| `wait <id>` | cmd/wait.go:42-96 | calls `detectResourceType`: tries trace first, then task; loops `pollState` every `--interval`s until terminal or `--timeout` | `GET /api/v2/traces/:id` then `GET /api/v2/tasks/:id` (probing); polls one of these |
| `status` | cmd/status.go:46-170 | profile + tasks + traces + health rollup | `GET /api/v2/auth/profile`, `GET /api/v2/tasks?page=1&size=100`, `GET /api/v2/traces?page=1&size=10`, `GET /system/health` |
| `eval list` | cmd/eval.go:45-75 | paginated list | `GET /api/v2/evaluations?page=&size=` |
| `eval get <id>` | cmd/eval.go:79-109 | GET detail | `GET /api/v2/evaluations/:id` |
| `pedestal helm get` | cmd/pedestal.go:70-100 | requires `--container-version-id` | `GET /api/v2/pedestal/helm/:cv_id` |
| `pedestal helm set` | cmd/pedestal.go:112-144 | upsert via PUT | `PUT /api/v2/pedestal/helm/:cv_id` |
| `pedestal helm verify` | cmd/pedestal.go:147-181 | POST verify, exit 1 if `!ok` | `POST /api/v2/pedestal/helm/:cv_id/verify` |
| `cluster preflight` | cmd/cluster.go:28-61 | `cluster.LoadConfig(--config)` → `cluster.NewLiveEnv(cfg)` → `cluster.NewRegistry(cluster.DefaultChecks())` → `Runner.Run`. Exits 1 on any failure. **No backend HTTP call** — talks directly to k8s/MySQL/Redis/etcd/ClickHouse | (none — out-of-band) |
| `rate-limiter status` | cmd/rate_limiter.go:70-110 | GET, render holders, mark `LEAKED` for terminal-state holders | `GET /api/v2/rate-limiters` |
| `rate-limiter reset` | cmd/rate_limiter.go:117-135 | requires `--force` | `DELETE /api/v2/rate-limiters/:bucket` |
| `rate-limiter gc` | cmd/rate_limiter.go:137-154 | POST | `POST /api/v2/rate-limiters/gc` |
| `completion {bash,zsh,fish}` | cmd/completion.go | calls `rootCmd.GenBashCompletion` / `GenZshCompletion` / `GenFishCompletion` | (none) |

### `inject` family — guided vs legacy: shared code path?

Verified: `inject_guided.go` and `inject.go` are **separate command leaves** (each adds itself to `injectCmd` in its own `init()`: `inject.go:784-792`, `inject_guided.go:287`). They share **no helpers** in the inject submission flow. Both write through the same backend endpoint `POST /api/v2/projects/:pid/injections/inject` though:

- `inject submit` (inject.go:213-242): POSTs the full `InjectSpec` (`{pedestal,benchmark,interval,pre_duration,specs:[[FaultSpec]],algorithms?,labels?}`) marshalled directly from YAML.
- `inject guided --apply` (inject_guided.go:295-341 `submitGuidedApply`): POSTs an envelope with `pedestal`, `benchmark`, `interval`, `pre_duration` from CLI flags (`--pedestal-name/-tag`, `--benchmark-name/-tag`, `--interval`, `--pre-duration`) and `specs:[[<rawCfg>]]` where `<rawCfg>` is the json-marshaled `guidedcli.GuidedConfig`. Per the source comment at `inject_guided.go:290-294`, the backend handler auto-detects the `chaos_type` key inside a spec entry and dispatches to `pkg/guidedcli` instead of the legacy DSL.

So the **CLI** code paths are disjoint; the **backend** route is the same single endpoint and the inner spec entry shape is what triggers the guided vs legacy interpretation.

`runInjectSubmitWait` (inject.go:247-321) and `runInjectWait` (inject_wait.go:95-232) are shared by `inject submit --wait`. Guided does NOT integrate with wait (no `--wait` flag exposed on `injectGuidedCmd`).

---

## 4. aegisctl → backend contract (deduped, grouped)

All endpoints actually invoked by `cmd/aegisctl/cmd/*.go` and `cmd/aegisctl/client/*.go`. (Resolver helpers in `client/resolver.go` are flagged.)

**Auth (REST)**
- `POST /api/v2/auth/login` — `client/auth.go:49`
- `POST /api/v2/auth/refresh` — `client/auth.go:69`
- `GET  /api/v2/auth/profile` — `client/auth.go:92`, `cmd/status.go:62`

**Projects (REST)**
- `GET  /api/v2/projects?page=&size=` — `cmd/project.go:56`, resolver `client/resolver.go:77` (page=1&size=100)
- `GET  /api/v2/projects/:id` — `cmd/project.go:93`
- `POST /api/v2/projects` — `cmd/project.go:133`

**Containers (REST)**
- `GET  /api/v2/containers?page=&size=[&type=]` — `cmd/container.go:70`, `cmd/container.go:302`, resolver `client/resolver.go:89`
- `GET  /api/v2/containers/:id` — `cmd/container.go:114`
- `GET  /api/v2/containers/:id/versions` — `cmd/container.go:162`, `cmd/container.go:312`, `cmd/container.go:347`
- `POST /api/v2/containers/build` — `cmd/container.go:391`
- `PATCH /api/v2/container-versions/:id/image` — `cmd/container.go:265`

**Datasets (REST)**
- `GET  /api/v2/datasets?page=&size=` — `cmd/dataset.go:53`, resolver `client/resolver.go:95`
- `GET  /api/v2/datasets/:id` — `cmd/dataset.go:87`
- `GET  /api/v2/datasets/:id/versions` — `cmd/dataset.go:122`

**Injections (REST + SSE)**
- `POST /api/v2/projects/:pid/injections/inject` — `cmd/inject.go:213` & `cmd/inject_guided.go:336`
- `GET  /api/v2/projects/:pid/injections?...` — `cmd/inject.go:350`
- `POST /api/v2/projects/:pid/injections/search` — `cmd/inject.go:453`
- `GET  /api/v2/injections?page=1&size=100` — resolver `client/resolver.go:83`
- `GET  /api/v2/injections/:id` — `cmd/inject.go:418`
- `GET  /api/v2/injections/:id/logs` — `cmd/inject.go:477`
- `GET  /api/v2/injections/:id/files` — `cmd/inject.go:507`
- `GET  /api/v2/injections/:id/download` — `cmd/inject.go:546` (raw HTTP, binary)
- `GET  /api/v2/injections/metadata[?system=]` — `cmd/inject.go:592`, `cmd/inject.go:679`

**Executions (REST)**
- `POST /api/v2/projects/:pid/executions/execute` — `cmd/execute.go:97`
- `GET  /api/v2/projects/:pid/executions?page=&size=` — `cmd/execute.go:135`
- `GET  /api/v2/executions/:id` — `cmd/execute.go:175`

**Tasks (REST + WebSocket)**
- `GET  /api/v2/tasks[?...]` — `cmd/task.go:55`, `cmd/status.go:78,112`
- `GET  /api/v2/tasks/:id` — `cmd/task.go:192`, `cmd/wait.go:111,136`
- `POST /api/v2/tasks/:id/expedite` — `cmd/task.go:135`
- `WS   /api/v2/tasks/:id/logs/ws?token=` — `cmd/task.go:221` (`client.NewWSReader`, see `client/ws.go:19-41` for ws:// upgrade + token query param)

**Traces (REST + SSE)**
- `GET  /api/v2/traces[?...]` — `cmd/trace.go:71`, `cmd/status.go:88,128`
- `GET  /api/v2/traces/:id` — `cmd/trace.go:122`, `cmd/wait.go:103,134`
- `GET  /api/v2/traces/:id/stream` — `cmd/inject.go:271`, `cmd/trace.go:177` (**SSE** via `client.NewSSEReader`, `client/sse.go:28-36`)

**Evaluations (REST)**
- `GET  /api/v2/evaluations?page=&size=` — `cmd/eval.go:50`
- `GET  /api/v2/evaluations/:id` — `cmd/eval.go:87`

**Pedestal helm (REST)**
- `GET  /api/v2/pedestal/helm/:cv_id` — `cmd/pedestal.go:79`
- `PUT  /api/v2/pedestal/helm/:cv_id` — `cmd/pedestal.go:133`
- `POST /api/v2/pedestal/helm/:cv_id/verify` — `cmd/pedestal.go:156`

**Rate limiters (REST)**
- `GET  /api/v2/rate-limiters` — `cmd/rate_limiter.go:76`
- `DELETE /api/v2/rate-limiters/:bucket` — `cmd/rate_limiter.go:129`
- `POST /api/v2/rate-limiters/gc` — `cmd/rate_limiter.go:143`

**System (REST, non-`/api/v2`)**
- `GET  /system/health` — `cmd/status.go:94,146`

**Out-of-band (no HTTP)**
- `cluster preflight` (cmd/cluster.go) — talks directly to k8s/MySQL/Redis/etcd/ClickHouse via `aegis/cmd/aegisctl/cluster.{LoadConfig,NewLiveEnv,NewRegistry,DefaultChecks,Runner}`.
- `inject guided` without `--apply` — only local YAML I/O via `pkg/guidedcli`.

**Endpoints declared by backend but NOT touched by aegisctl** (high-signal subset for the reducer): `/api/v2/auth/{register,logout,refresh,change-password}`, every `/api/v2/teams/*`, `/api/v2/labels/*`, `/api/v2/users/*`, `/api/v2/roles/*`, `/api/v2/permissions/*`, `/api/v2/resources/*`, `/api/v2/groups/*`, `/api/v2/notifications/*`, `/api/v2/sdk/*`, `/api/v2/metrics/*`, `/api/v2/systems/*`, `/api/v2/system/metrics*`, all `/system/audit*`, `/system/configs*`, `/system/monitor/*`, `/api/v2/injections/{upload,systems,translate,/:id/files/download,/:id/files/query,/:id/clone,/:id/groundtruth,labels/batch,batch-delete}`, `/api/v2/executions/{:id/detector_results,:id/granularity_results,:id/labels,batch-delete,/labels}`, `/api/v2/evaluations/{datasets,datapacks}`, `/api/v2/datasets/:id/versions/...` (only top list is hit), `/api/v2/projects/:pid/injections/{build,analysis/no-issues,analysis/with-issues}`.

---

## 5. Config loading & hot reload

`config.Init(configPath string)` (`config/config.go:37-80`):
1. Reads env `ENV` (default `dev`); sets viper config-name = `config.<env>`, type = `toml`.
2. Search paths: `configPath` (the `--conf` flag value, used as `viper.AddConfigPath`), `$HOME/.rcabench`, `/etc/rcabench`, `.`.
3. `viper.ReadInConfig()`; on failure, dumps the bad file's text and either logs a parse error or fatals.
4. Calls `viper.AutomaticEnv()` so any viper key can be overridden via env.
5. Calls `validate()` (`config/config.go:181-239`) — fatals if any of `name, version, port, workspace, database.mysql.{host,port,user,password,db}, redis.host, jaeger.endpoint, k8s.namespace, k8s.job.service_account.name` are missing, or port outside `1..65535`.

**Note**: `--conf` is documented in `main.go:82` as `"Path to configuration file"` but `viper.AddConfigPath` expects a directory; passing a file path will silently miss unless that path is a dir containing `config.<env>.toml`.

Typed accessors: `Get/GetString/GetInt/GetBool/GetFloat64/GetStringSlice/GetIntSlice/GetMap/GetList` (`config/config.go:82-132`). `SetViperValue(key, value, valueType)` (`config/config.go:134-178`) supports runtime mutation by `consts.ConfigValueType` enum (`String/Bool/Int/Float/StringArray`); used by the dynamic-config handlers.

**Atomic state in `config/config.go`:**
- `var detectorName atomic.Value` (`config/config.go:17`); `GetDetectorName` (line 21) / `SetDetectorName` (line 31) are runtime-mutable. Consumed by: `dto/{injection.go,evaluation.go,execution.go}`, `service/producer/{trace.go,execution.go}`, `service/consumer/{collect_result.go,k8s_handler.go,algo_execution.go}`, `service/initialization/common.go`, `service/common/config_registry.go` (per Grep `SetDetectorName|GetDetectorName`).

**`config/chaos_system.go` (runtime-mutable singleton):**
- `chaosConfigDB *gorm.DB` set via `SetChaosConfigDB(db)` (`chaos_system.go:21-23`) — package-level mutable; bound from outside (db package) to break a circular import.
- `chaosSystemConfigManager` singleton initialized lazily by `GetChaosSystemConfigManager()` (`chaos_system.go:46-61`); contains `map[chaos.SystemType]ChaosSystemConfig` guarded by `sync.RWMutex`. Methods: `Get`, `GetAll` (defensive copy), `Reload(callback)` (re-runs `load()` then runs callback), and `load()` (`chaos_system.go:94-125`) which: tries `loadFromSystemTable` (gorm `Table("systems").Where("status = 1")`) first, else falls back to `viper`-backed `injection.system` map, decoded via `mapstructure`.
- `GetAllNamespaces()` (`chaos_system.go:195-214`) — generates namespace list using `convertPatternToTemplate(NsPattern)` (regex→sprintf template, `chaos_system.go:224-238`) and `Count`.
- `ChaosSystemConfig.ExtractNsNumber(namespace)` (`chaos_system.go:171-192`) — parses ns number from regex `ExtractPattern` capture group #2.

**Outside-of-slice consumers of `config/chaos_system.go`** (Grep `GetChaosSystemConfigManager|SetChaosConfigDB|GetAllNamespaces|ChaosSystemConfig`): `service/initialization/systems.go`, `service/consumer/{monitor.go, restart_pedestal.go, config_handlers.go}`. (Plus reflexive use inside `config/chaos_system.go` itself.)

No file watcher / SIGHUP / `viper.WatchConfig` is wired anywhere in this slice — viper config is loaded once. Hot-reload of chaos systems is **pull-based**: callers invoke `manager.Reload(callback)`.

---

## 6. Cross-slice outbound call list

Edges leaving this slice (deduped). Form: `<source>` → `<external symbol>`.

**main.go**
- main.go → `aegis/client.InitTraceProvider`
- main.go → `aegis/client/k8s.GetK8sRestConfig`
- main.go → `aegis/client/k8s.GetK8sController().Initialize`
- main.go → `aegis/consts.{InitialTime, AppID}` (assigned)
- main.go → `aegis/database.InitDB`
- main.go → `aegis/router.New`
- main.go → `aegis/service/consumer.NewHandler`
- main.go → `aegis/service/consumer.StartScheduler`
- main.go → `aegis/service/consumer.ConsumeTasks`
- main.go → `aegis/service/initialization.{InitializeProducer, InitializeConsumer, InitConcurrencyLock}`
- main.go → `aegis/service/logreceiver.{NewOTLPLogReceiver, DefaultPort}` then `.Start(ctx)` / `.Shutdown()`
- main.go → `aegis/service/producer.GCRateLimiters`
- main.go → `aegis/utils.{InitValidator, TimePtr, GenerateULID}`
- main.go → `github.com/OperationsPAI/chaos-experiment/client.InitWithConfig`

**router/router.go**
- → `aegis/middleware.{GroupID, SSEPath, TracerMiddleware}` (global)
- → `github.com/gin-contrib/cors`, `github.com/swaggo/{files, gin-swagger}`

**router/system.go** — `aegis/handlers/system.{ListAuditLogs, GetAuditLog, ListConfigs, GetConfig, ListConfigHistories, UpdateConfigValue, RollbackConfigValue, UpdateConfigMetadata, RollbackConfigMetadata, GetHealth, GetMetrics, GetSystemInfo, ListNamespaceLocks, ListQueuedTasks}`; `aegis/middleware.{JWTAuth, RequireAuditRead, RequireConfigurationRead, RequireConfigurationUpdate, RequireConfigurationConfigure, RequireSystemRead}`.

**router/v2.go** — `aegis/middleware.StartCleanupRoutine`, `aegis/middleware.JWTAuth`, plus all `Require*` middlewares listed in §2b. Handlers `aegis/handlers/v2.*`: every name in §2b (Login, Register, RefreshToken, Logout, ChangePassword, GetProfile, GetContainer, ListContainers, CreateContainer, SubmitContainerBuilding, UpdateContainer, ManageContainerCustomLabels, DeleteContainer, GetContainerVersion, ListContainerVersions, CreateContainerVersion, UploadHelmChart, UploadHelmValueFile, UpdateContainerVersion, DeleteContainerVersion, SetContainerVersionImage, GetDataset, ListDatasets, CreateDataset, UpdateDataset, ManageDatasetCustomLabels, DeleteDataset, ListDatasetVersions, GetDatasetVersion, DownloadDatasetVersion, CreateDatasetVersion, UpdateDatasetVersion, ManageDatasetVersionInjections, DeleteDatasetVersion, GetProjectDetail, ListProjects, CreateProject, UpdateProject, ManageProjectCustomLabels, DeleteProject, ListFaultInjectionNoIssues, ListFaultInjectionWithIssues, ListProjectInjections, SearchInjections, SubmitProjectFaultInjection, SubmitProjectDatapackBuilding, ListProjectExecutions, SubmitAlgorithmExecution, CreateTeam, ListTeams, UpdateTeam, DeleteTeam, AddTeamMember, RemoveTeamMember, UpdateTeamMemberRole, GetTeamDetail, ListTeamMembers, ListTeamProjects, GetLabelDetail, ListLabels, CreateLabel, UpdateLabel, DeleteLabel, BatchDeleteLabels, ListUsersV2, GetUserDetailV2, CreateUser, UpdateUser, DeleteUser, AssignUserRole, RemoveGlobalRole, AssignUserProject, RemoveUserProject, AssignUserPermission, RemoveUserPermission, AssignUserContainer, RemoveUserContainer, AssignUserDataset, RemoveUserDataset, AssignRolePermission, RemovePermissionsFromRole, ListUsersFromRole, GetRole, ListRoles, CreateRole, UpdateRole, DeleteRole, ListRolesFromPermission, ListPermissions, GetPermission, ListResourcePermissions, GetResourceDetail, ListResources, ListTasks, GetTask, BatchDeleteTasks, ExpediteTask, GetTaskLogsWS, ListInjections, UploadDatapack, GetSystemMapping, TranslateFaultSpecs, GetInjection, DownloadDatapack, GetInjectionLogs, ListDatapackFiles, DownloadDatapackFile, QueryDatapackFile, GetInjectionMetadata, CloneInjection, UpdateGroundtruth, ManageInjectionCustomLabels, BatchManageInjectionLabels, BatchDeleteInjections, ListExecutions, ListAvaliableExecutionLabels, GetExecution, UploadDetectorResults, UploadGranularityResults, ManageExecutionCustomLabels, BatchDeleteExecutions, ListTraces, GetTrace, GetTraceStream, GetAlgorithmMetrics, GetGroupStream, GetNotificationStream, ListEvaluations, GetEvaluation, DeleteEvaluation, ListDatasetEvaluationResults, ListDatapackEvaluationResults, ListSDKEvaluations, ListSDKExperiments, GetSDKEvaluation, ListSDKDatasetSamples, GetInjectionMetrics, GetExecutionMetrics, GetPedestalHelmConfig, VerifyPedestalHelmConfig, UpsertPedestalHelmConfig, ListChaosSystemsHandler, CreateChaosSystemHandler, GetChaosSystemHandler, UpdateChaosSystemHandler, DeleteChaosSystemHandler, UpsertChaosSystemMetadataHandler, ListChaosSystemMetadataHandler, GetSystemMetrics, GetSystemMetricsHistory, ListRateLimiters, ResetRateLimiter, GCRateLimiters)`.

**config/config.go** — `aegis/consts.{ConfigValueType*}` (enum decode); `github.com/spf13/viper`, logrus.

**config/chaos_system.go** — `github.com/OperationsPAI/chaos-experiment/handler.SystemType`, `github.com/mitchellh/mapstructure`, `gorm.io/gorm`. Read by external pkgs listed in §5.

**cmd/aegisctl/main.go** — `aegis/cmd/aegisctl/cmd.Execute`.

**cmd/aegisctl/cmd/** — `aegis/cmd/aegisctl/{client, config, output, cluster}`; `github.com/OperationsPAI/chaos-experiment/pkg/guidedcli.{LoadConfig, MergeConfig, Resolve, ApplyNextSelection, SaveConfig, GuidedConfig, GuidedSession}` (only from `inject_guided.go`).

**cmd/aegisctl/client/** — `github.com/gorilla/websocket` (ws.go), stdlib `net/http`, `bufio`, `encoding/json`.

**cmd/aegisctl/cluster/** — direct k8s/sql/clickhouse/redis/etcd connections (out-of-process); not invoked via the AegisLab HTTP API.

---

## 7. Surprises / dead code

1. **`analyzer` route group is a no-op.** `v2.go:582-583` declares `analyzer := v2.Group("/analyzer", JWTAuth())` then immediately discards it with `_ = analyzer`. Comment self-acknowledges "Temporarily unused". Wastes one middleware allocation per startup.
2. **`SearchInjections` handler is bound to two distinct routes** (`POST /api/v2/projects/:pid/injections/search` at `v2.go:248` and `POST /api/v2/injections/search` at `v2.go:499` under SystemAdmin). Same handler symbol in both — auth check must therefore live inside the handler too, or the SystemAdmin gate is the only protection on the global variant.
3. **`/translate` and `/metadata` injection routes are still wired** (`v2.go:506-507`, 516) — contradicts the user-memory note saying they return `410`. Either the memory note is stale or the 410 check lives inside the handlers (out of slice).
4. **`--conf` flag semantics mismatch.** `main.go:82` advertises a *file path* default (`/etc/rcabench/config.prod.toml`), but `config.Init` passes the value to `viper.AddConfigPath` (`config/config.go:46-48`), which expects a *directory*. The flag will silently fall through to the other search paths if the user passes a file, unless that file's parent directory contains `config.<env>.toml`.
5. **`runRateLimiterStartupGC` only runs in `consumer` and `both` modes** (`main.go:137, 179`) — `producer` mode never reclaims leaked tokens. With pure-producer deployments, leaked Redis bucket tokens accumulate until a consumer joins.
6. **`producer` mode skips `initChaosExperiment` ordering**: it calls `client.InitTraceProvider` then `initChaosExperiment` (`main.go:106-107`) but does NOT spin up the `k8s.GetK8sController().Initialize` goroutine — only `consumer` and `both` do. Producer therefore can't observe CRD/Job lifecycle callbacks.
7. **`config.GetDetectorName` panics-via-nil-on-error if not initialized** — it logs `"Detector name not initialized yet"` and returns `""` (`config/config.go:25-27`). Callers that don't check for empty string will silently use empty algorithm name.
8. **`fetchContainerVersionByID` (cmd/container.go:300-322) is O(N×M)** — scans all containers, resolves each name, lists each container's versions. For a CLI-side dry-run it issues `1 + N + N` HTTP calls. Adequate for small registries, latent on large ones.
9. **`container versions <name>` renders the same `ImageRef` value into two columns** (`Image` and `IMAGE` at `cmd/container.go:173`) "for backward compatibility". That's two identical columns in every row.
10. **`task logs --follow=false` waits exactly 5s then exits** (`cmd/task.go:257-275`), which can race against logs that take longer to start streaming on the WebSocket. There's no "no logs yet" signal.

---

End of slice 01.
