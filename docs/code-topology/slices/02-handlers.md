# AegisLab Backend Topology — Handler + Middleware Slice

Source-of-truth: `src/handlers/` and `src/middleware/`.
File counts: handlers root (4), system (4), v2 (24 .go + `pedestalhelm/`), middleware (5).

---

## 1. Handler directory → domain table

### handlers/ (root, package `handlers`)
| File | Domain | Exported handlers |
|---|---|---|
| common.go:16,49 | shared helpers | `ParsePositiveID`, `HandleServiceError` (no @Router) |
| debug.go:12,16,32 | runtime debug-vars (no Router; mounted manually) | `GetAllVars`, `GetVar`, `SetVar` |
| docs.go:38 | swagger model holder (empty body) | `SwaggerModelsDoc` |

### handlers/system/ (package `system`)
| File | Domain | Exported handlers |
|---|---|---|
| audit.go | audit log read | `GetAuditLog`, `ListAuditLogs` |
| configs.go | dynamic config CRUD + history | `GetConfig`, `ListConfigs`, `RollbackConfigValue`, `RollbackConfigMetadata`, `UpdateConfigValue`, `UpdateConfigMetadata`, `ListConfigHistories` |
| health.go | liveness probes | `GetHealth` (+ unexported per-service checkers) |
| monitor.go | system monitor (`/system/monitor/*`) | `GetMetrics` (deprecated), `GetSystemInfo`, `ListNamespaceLocks`, `ListQueuedTasks` |

### handlers/v2/ (package `v2`)
| File | Domain | Exported handlers |
|---|---|---|
| auth.go | login/JWT/profile | `Register`, `Login`, `RefreshToken`, `Logout`, `ChangePassword`, `GetProfile` |
| containers.go | container + container_version + helm chart upload | `CreateContainer`, `DeleteContainer`, `GetContainer`, `ListContainers`, `UpdateContainer`, `CreateContainerVersion`, `DeleteContainerVersion`, `GetContainerVersion`, `ListContainerVersions`, `UpdateContainerVersion`, `SetContainerVersionImage`, `ManageContainerCustomLabels`, `SubmitContainerBuilding`, `UploadHelmChart`, `UploadHelmValueFile` |
| datasets.go | dataset + dataset_version | `CreateDataset`, `DeleteDataset`, `GetDataset`, `ListDatasets`, `SearchDataset`, `UpdateDataset`, `ManageDatasetCustomLabels`, `CreateDatasetVersion`, `DeleteDatasetVersion`, `GetDatasetVersion`, `ListDatasetVersions`, `UpdateDatasetVersion`, `DownloadDatasetVersion`, `ManageDatasetVersionInjections` |
| evaluations.go | evaluation results & CRUD | `ListDatapackEvaluationResults`, `ListDatasetEvaluationResults`, `ListEvaluations`, `GetEvaluation`, `DeleteEvaluation` |
| executions.go | algorithm execution submit/query | `BatchDeleteExecutions`, `GetExecution`, `ListExecutions`, `ListAvaliableExecutionLabels`, `ManageExecutionCustomLabels`, `SubmitAlgorithmExecution`, `UploadDetectorResults`, `UploadGranularityResults` |
| groups.go | group stats + SSE | `GetGroupStats`, `GetGroupStream` |
| injections.go | fault injection / datapack | `BatchDeleteInjections`, `GetInjection`, `GetInjectionMetadata` (410), `ListInjections`, `SearchInjections`, `ListFaultInjectionNoIssues`, `ListFaultInjectionWithIssues`, `ManageInjectionCustomLabels`, `BatchManageInjectionLabels`, `SubmitFaultInjection`, `SubmitDatapackBuilding`, `CloneInjection`, `GetInjectionLogs`, `DownloadDatapack`, `ListDatapackFiles`, `DownloadDatapackFile`, `QueryDatapackFile`, `GetSystemMapping`, `TranslateFaultSpecs` (410), `UploadDatapack`, `UpdateGroundtruth` |
| labels.go | label CRUD | `BatchDeleteLabels`, `CreateLabel`, `DeleteLabel`, `GetLabelDetail`, `ListLabels`, `UpdateLabel` |
| metrics.go | injection/execution/algo metrics | `GetInjectionMetrics`, `GetExecutionMetrics`, `GetAlgorithmMetrics` |
| notifications.go | global SSE notifications | `GetNotificationStream` |
| pedestal_helm.go | helm config CRUD + dry-run verify | `GetPedestalHelmConfig`, `UpsertPedestalHelmConfig`, `VerifyPedestalHelmConfig` (+ unexported `parsePedestalVersionID`, `toHelmConfigResp`) |
| pedestalhelm/verify.go | pure helm-CLI dry-run pipeline (subpkg) | `Run`, `VerifyValueFile`, `Config`, `Check`, `Result`, `Runner`, `RealRunner` |
| permissions.go | permission read | `GetPermission`, `ListPermissions`, `ListRolesFromPermission` |
| projects.go | project CRUD + project-scoped injections/executions | `CreateProject`, `DeleteProject`, `GetProjectDetail`, `ListProjects`, `UpdateProject`, `ManageProjectCustomLabels`, `ListProjectInjections`, `SearchProjectInjections`, `ListProjectFaultInjectionNoIssues`, `ListProjectFaultInjectionWithIssues`, `SubmitProjectFaultInjection`, `SubmitProjectDatapackBuilding`, `ListProjectExecutions` |
| rate_limiters.go | runtime rate-limiter buckets | `ListRateLimiters`, `ResetRateLimiter`, `GCRateLimiters` |
| resources.go | RBAC resource introspection | `GetResourceDetail`, `ListResources`, `ListResourcePermissions` |
| roles.go | role CRUD + permission grant | `CreateRole`, `DeleteRole`, `GetRole`, `ListRoles`, `UpdateRole`, `AssignRolePermission`, `RemovePermissionsFromRole` |
| sdk_evaluations.go | SDK evaluation read | `ListSDKEvaluations`, `GetSDKEvaluation`, `ListSDKExperiments`, `ListSDKDatasetSamples` |
| system.go | live system metrics | `GetSystemMetrics`, `GetSystemMetricsHistory` |
| systems.go | chaos system catalog | `ListChaosSystemsHandler`, `GetChaosSystemHandler`, `CreateChaosSystemHandler`, `UpdateChaosSystemHandler`, `DeleteChaosSystemHandler`, `UpsertChaosSystemMetadataHandler`, `ListChaosSystemMetadataHandler` |
| tasks.go | task queue + WS logs | `BatchDeleteTasks`, `GetTask`, `ListTasks`, `ExpediteTask`, `GetTaskLogsWS` |
| teams.go | team CRUD + members + projects | `CreateTeam`, `DeleteTeam`, `GetTeamDetail`, `ListTeams`, `UpdateTeam`, `ListTeamProjects`, `AddTeamMember`, `RemoveTeamMember`, `UpdateTeamMemberRole`, `ListTeamMembers` |
| traces.go | trace read + SSE | `GetTrace`, `ListTraces`, `GetTraceStream` |
| users.go | user CRUD + role/permission/resource assignment | `CreateUser`, `DeleteUser`, `GetUserDetailV2`, `ListUsersV2`, `UpdateUser`, `AssignUserRole`, `RemoveGlobalRole`, `ListUsersFromRole`, `AssignUserPermission`, `RemoveUserPermission`, `AssignUserContainer`, `RemoveUserContainer`, `AssignUserDataset`, `RemoveUserDataset`, `AssignUserProject`, `RemoveUserProject` |

---

## 2. Per-handler call matrix

Common conventions: `Auth = JWT` whenever Swagger annotation says `@Security BearerAuth`; service tokens accepted via `middleware.JWTAuth` (auth.go:14). Permission middlewares listed are the named gating vars from `middleware/permission.go` (registered at the route level by the router; routes themselves live outside this slice).

Abbreviations: `BA`=BearerAuth, `BAReq`=BatchDeleteReq, `Resp`=GenericResponse[T].

### handlers/v2/auth.go (auth.go)
| Func | Verb + Route | Req DTO | Resp DTO | Service calls | Auth |
|---|---|---|---|---|---|
| Register | POST /api/v2/auth/register | dto.RegisterReq | dto.RegisterResp | `producer.Register` | none (public) |
| Login | POST /api/v2/auth/login | dto.LoginReq | dto.LoginResp | `producer.Login` | none |
| RefreshToken | POST /api/v2/auth/refresh | dto.RefreshTokenReq | dto.RefreshTokenResp | `producer.RefreshToken` | none |
| Logout | POST /api/v2/auth/logout | – | – | `utils.ValidateToken` then `producer.Logout(ctx, claims)` | BA |
| ChangePassword | POST /api/v2/auth/change-password | dto.ChangePasswordReq | – | `producer.ChangePassword` (uses `middleware.GetCurrentUserID`) | BA |
| GetProfile | GET /api/v2/auth/profile | – | dto.UserProfileResp | `producer.GetProfile` | BA |

### handlers/v2/users.go
| Func | Verb + Route | Req DTO | Resp DTO | Service calls | Auth |
|---|---|---|---|---|---|
| CreateUser | POST /api/v2/users | dto.CreateUserReq | dto.UserDetailResp | `producer.CreateUser` | BA + `RequireUserCreate` |
| DeleteUser | DELETE /api/v2/users/{id} | – | – | `producer.DeleteUser` | BA + `RequireUserDelete` |
| GetUserDetailV2 | GET /api/v2/users/{id}/detail | – | dto.UserDetailResp | `producer.GetUserDetail` | BA + `RequireUserRead` |
| ListUsersV2 | GET /api/v2/users | dto.ListUserReq | dto.ListResp[UserResp] | `producer.ListUsers` | BA + `RequireUserRead` |
| UpdateUser | PATCH /api/v2/users/{id} | dto.UpdateUserReq | dto.UserDetailResp | `producer.UpdateUser` | BA + `RequireUserUpdate` |
| AssignUserRole | POST /api/v2/users/{user_id}/role/{role_id} | – | – | `producer.AssignRoleToUser` | BA + `RequireRoleGrant` |
| RemoveGlobalRole | DELETE /api/v2/users/{user_id}/roles/{role_id} | – | – | `producer.RemoveRoleFromUser` | BA + `RequireRoleRevoke` |
| ListUsersFromRole | GET /api/v2/roles/{role_id}/users | – | []dto.UserResp | `producer.ListUsersFromRole` | BA + `RequireUserRead` |
| AssignUserPermission | POST /api/v2/users/{user_id}/permissions/assign | dto.BatchAssignUserPermissionsReq | – | `producer.BatchAssignUserPermissions` | BA + `RequireUserAssign` |
| RemoveUserPermission | POST /api/v2/users/{user_id}/permissions/remove | dto.BatchRemoveUserPermissionsReq | – | `producer.BatchRemoveUserPermissions` | BA + `RequireUserAssign` |
| AssignUserContainer | POST /api/v2/users/{user_id}/containers/{container_id}/roles/{role_id} | – | – | `producer.AssignContainerToUser` | BA + `RequireRoleGrant` |
| RemoveUserContainer | DELETE /api/v2/users/{user_id}/containers/{container_id} | – | – | `producer.RemoveContainerFromUser` | BA + `RequireRoleRevoke` |
| AssignUserDataset | POST /api/v2/users/{user_id}/datasets/{dataset_id}/roles/{role_id} | – | – | `producer.AssignDatasetToUser` | BA + `RequireRoleGrant` |
| RemoveUserDataset | DELETE /api/v2/users/{user_id}/datasets/{dataset_id} | – | – | `producer.RemoveDatasetFromUser` | BA + `RequireRoleRevoke` |
| AssignUserProject | POST /api/v2/users/{user_id}/projects/{project_id}/roles/{role_id} | – | – | `producer.AssignProjectToUser` | BA + `RequireRoleGrant` |
| RemoveUserProject | DELETE /api/v2/users/{user_id}/projects/{project_id} | – | – | `producer.RemoveProjectFromUser` | BA + `RequireRoleRevoke` |

### handlers/v2/teams.go
| Func | Verb + Route | Req DTO | Resp DTO | Service calls | Auth |
|---|---|---|---|---|---|
| CreateTeam | POST /api/v2/teams | dto.CreateTeamReq | dto.TeamDetailResp | `producer.CreateTeam` | BA + `RequireTeamCreate` |
| DeleteTeam | DELETE /api/v2/teams/{team_id} | – | – | `producer.DeleteTeam` | BA + `RequireTeamAdminAccess` |
| GetTeamDetail | GET /api/v2/teams/{team_id} | – | dto.TeamDetailResp | `producer.GetTeamDetail` | BA + `RequireTeamMemberAccess` |
| ListTeams | GET /api/v2/teams | dto.ListTeamReq | dto.ListResp[TeamResp] | `producer.ListTeams(req,userID,isAdmin)` | BA |
| UpdateTeam | PATCH /api/v2/teams/{team_id} | dto.UpdateTeamReq | dto.TeamDetailResp | `producer.UpdateTeam` | BA + `RequireTeamAdminAccess` |
| ListTeamProjects | GET /api/v2/teams/{team_id}/projects | dto.ListProjectReq | dto.ListResp[ProjectResp] | `producer.ListTeamProjects` | BA + `RequireTeamMemberAccess` |
| AddTeamMember | POST /api/v2/teams/{team_id}/members | dto.AddTeamMemberReq | – | `producer.AddTeamMember` | BA + `RequireTeamAdminAccess` |
| RemoveTeamMember | DELETE /api/v2/teams/{team_id}/members/{user_id} | – | – | `producer.RemoveTeamMember` | BA + `RequireTeamAdminAccess` |
| UpdateTeamMemberRole | PATCH /api/v2/teams/{team_id}/members/{user_id}/role | dto.UpdateTeamMemberRoleReq | – | `producer.UpdateTeamMemberRole` | BA + `RequireTeamAdminAccess` |
| ListTeamMembers | GET /api/v2/teams/{team_id}/members | dto.ListTeamMemberReq | dto.ListResp[TeamMemberResp] | `producer.ListTeamMembers` | BA + `RequireTeamMemberAccess` |

### handlers/v2/projects.go
| Func | Verb + Route | Req DTO | Resp DTO | Service calls | Auth |
|---|---|---|---|---|---|
| CreateProject | POST /api/v2/projects | dto.CreateProjectReq | dto.ProjectDetailResp | `producer.CreateProject` | BA + `RequireProjectCreate` |
| DeleteProject | DELETE /api/v2/projects/{project_id} | – | – | `producer.DeleteProject` | BA + `RequireProjectAdminAccess` |
| GetProjectDetail | GET /api/v2/projects/{project_id} | – | dto.ProjectDetailResp | `producer.GetProjectDetail` | BA + `RequireProjectMemberAccess` |
| ListProjects | GET /api/v2/projects | dto.ListProjectReq | dto.ListResp[ProjectResp] | `producer.ListProjects` | BA + `RequireProjectRead` |
| UpdateProject | PATCH /api/v2/projects/{project_id} | dto.UpdateProjectReq | dto.ProjectDetailResp | `producer.UpdateProject` | BA + `RequireProjectAdminAccess` |
| ManageProjectCustomLabels | PATCH /api/v2/projects/{project_id}/labels | dto.ManageLabelsReq | – | `producer.ManageProjectLabels` | BA + `RequireProjectAdminAccess` |
| ListProjectInjections | GET /api/v2/projects/{project_id}/injections | dto.ListInjectionReq | dto.ListResp[InjectionResp] | `producer.ListProjectInjections` | BA + `RequireProjectMemberAccess` |
| SearchProjectInjections | POST /api/v2/projects/{project_id}/injections/search | dto.SearchInjectionReq | dto.ListResp | `producer.SearchInjections` (via `searchInjectionsCommon`) | BA + `RequireProjectMemberAccess` |
| ListProjectFaultInjectionNoIssues | GET /api/v2/projects/{project_id}/injections/analysis/no-issues | dto.ListInjectionNoIssuesReq | []dto.InjectionNoIssuesResp | `producer.ListInjectionsNoIssues` | BA + `RequireProjectMemberAccess` |
| ListProjectFaultInjectionWithIssues | GET /api/v2/projects/{project_id}/injections/analysis/with-issues | dto.ListInjectionWithIssuesReq | []dto.InjectionWithIssuesResp | `producer.ListInjectionsWithIssues` | BA + `RequireProjectMemberAccess` |
| SubmitProjectFaultInjection | POST /api/v2/projects/{project_id}/injections/inject | dto.SubmitInjectionReq | dto.SubmitInjectionResp | `producer.FriendlySpecToNode`, `producer.ProduceRestartPedestalTasks` | BA + `RequireProjectInjectionExecute` |
| SubmitProjectDatapackBuilding | POST /api/v2/projects/{project_id}/injections/build | dto.SubmitDatapackBuildingReq | dto.SubmitDatapackBuildingResp | `producer.ProduceDatapackBuildingTasks` | BA + `RequireProjectInjectionExecute` |
| ListProjectExecutions | GET /api/v2/projects/{project_id}/executions | dto.ListExecutionReq | dto.ListResp[ExecutionResp] | `producer.ListProjectExecutions` | BA + `RequireProjectMemberAccess` |

### handlers/v2/injections.go (24 handlers, grouped)

CRUD/query group:
| Func | Verb + Route | Req DTO | Resp DTO | Service | Auth |
|---|---|---|---|---|---|
| BatchDeleteInjections | POST /api/v2/injections/batch-delete | dto.BatchDeleteByIDsAndLabelsReq | – | `producer.BatchDeleteInjectionsByIDs` / `…ByLabels` | BA |
| GetInjection | GET /api/v2/injections/{id} | – | dto.InjectionDetailResp | `producer.GetInjectionDetail` | BA |
| GetInjectionMetadata | GET /api/v2/injections/metadata | – | – | **410 Gone** (legacy) | BA |
| ListInjections | GET /api/v2/injections | dto.ListInjectionReq | dto.ListResp[InjectionResp] | `producer.ListInjections` | BA |
| SearchInjections | POST /api/v2/injections/search | dto.SearchInjectionReq | dto.ListResp | `producer.SearchInjections` | BA |
| ListFaultInjectionNoIssues | GET /api/v2/injections/analysis/no-issues | dto.ListInjectionNoIssuesReq | []dto.InjectionNoIssuesResp | `producer.ListInjectionsNoIssues` | BA |
| ListFaultInjectionWithIssues | GET /api/v2/injections/analysis/with-issues | dto.ListInjectionWithIssuesReq | []dto.InjectionWithIssuesResp | `producer.ListInjectionsWithIssues` | BA |
| ManageInjectionCustomLabels | PATCH /api/v2/injections/{id}/labels | dto.ManageLabelsReq | – | `producer.ManageInjectionLabels` | BA |
| BatchManageInjectionLabels | PATCH /api/v2/injections/labels/batch | dto.BatchManageLabelsReq | – | `producer.BatchManageInjectionLabels` | BA |

Submit/lifecycle group:
| Func | Verb + Route | Req DTO | Resp DTO | Service | Auth |
|---|---|---|---|---|---|
| SubmitFaultInjection | POST /api/v2/injections/inject | dto.SubmitInjectionReq | dto.SubmitInjectionResp | `producer.FriendlySpecToNode`, `producer.ProduceRestartPedestalTasks` (via `submitFaultInjectionCommon`, projects.go:340) | BA |
| SubmitDatapackBuilding | POST /api/v2/injections/build | dto.SubmitDatapackBuildingReq | dto.SubmitDatapackBuildingResp | `producer.ProduceDatapackBuildingTasks` | BA |
| CloneInjection | POST /api/v2/injections/{id}/clone | dto.CloneInjectionReq | dto.InjectionDetailResp | `producer.CloneInjection` | BA |
| GetInjectionLogs | GET /api/v2/injections/{id}/logs | – | dto.InjectionLogsResp | `producer.GetInjectionLogs` | BA |
| UploadDatapack | POST /api/v2/injections/upload | dto.UploadDatapackReq (multipart) | dto.UploadDatapackResp | `producer.UploadDatapack` | BA |
| UpdateGroundtruth | PUT /api/v2/injections/{id}/groundtruth | dto.UpdateGroundtruthReq | – | `producer.UpdateGroundtruth` | BA |

Datapack file/download group:
| Func | Verb + Route | Req DTO | Resp DTO | Service | Auth |
|---|---|---|---|---|---|
| DownloadDatapack | GET /api/v2/injections/{id}/download | – | zip stream | `producer.GetDatapackFilename`, `producer.DownloadDatapack` | BA |
| ListDatapackFiles | GET /api/v2/injections/{id}/files | – | dto.DatapackFilesResp | `producer.GetDatapackFiles(id, baseURL)` | BA |
| DownloadDatapackFile | GET /api/v2/injections/{id}/files/download | – | binary (Range support) | `producer.DownloadDatapackFile`, helper `serveRangeRequest` | BA |
| QueryDatapackFile | GET /api/v2/injections/{id}/files/query | – | parquet/duckdb stream | `producer.QueryDatapackFileContent` | BA |

Misc:
| Func | Verb + Route | Req | Resp | Service | Auth |
|---|---|---|---|---|---|
| GetSystemMapping | GET /api/v2/injections/systems | – | dto.SystemMappingResp | `chaos.GetAllSystemTypes`, `utils.BuildSystemIndexMap` (no service-layer call) | BA |
| TranslateFaultSpecs | POST /api/v2/injections/translate | dto.TranslateFaultSpecsReq | – | **410 Gone** (deprecated; injections.go:677) | BA |

### handlers/v2/executions.go
| Func | Verb + Route | Req | Resp | Service | Auth |
|---|---|---|---|---|---|
| BatchDeleteExecutions | POST /api/v2/executions/batch-delete | dto.BatchDeleteByIDsAndLabelsReq | – | `producer.BatchDeleteExecutionsByIDs/ByLabels` | BA |
| GetExecution | GET /api/v2/executions/{id} | – | dto.ExecutionDetailResp | `producer.GetExecutionDetail` | BA |
| ListExecutions | GET /api/v2/executions | dto.ListExecutionReq | dto.ListResp[ExecutionResp] | `producer.ListExecutions` | BA |
| ListAvaliableExecutionLabels | GET /api/v2/executions/labels | – | []string | `producer.ListAvaliableExecutionLabels` | BA |
| ManageExecutionCustomLabels | PATCH /api/v2/executions/{id}/labels | dto.ManageLabelsReq | – | `producer.ManageExecutionLabels` | BA |
| SubmitAlgorithmExecution | POST /api/v2/executions/execute | dto.SubmitExecutionReq | dto.SubmitExecutionResp | `producer.ProduceAlgorithmExeuctionTasks` (sic) | BA |
| UploadDetectorResults | POST /api/v2/executions/{execution_id}/detector_results | dto.BatchDetectorResultReq | – | `producer.BatchCreateDetectorResults` | BA (service token allowed) |
| UploadGranularityResults | POST /api/v2/executions/{execution_id}/granularity_results | dto.BatchGranularityResultReq | – | `producer.BatchCreateGranularityResults` | BA (service token allowed) |

### handlers/v2/datasets.go
| Func | Verb + Route | Req | Resp | Service | Auth |
|---|---|---|---|---|---|
| CreateDataset | POST /api/v2/datasets | dto.CreateDatasetReq | dto.DatasetDetailResp | `producer.CreateDataset` | BA + `RequireDatasetCreate` |
| DeleteDataset | DELETE /api/v2/datasets/{dataset_id} | – | – | `producer.DeleteDataset` | BA + `RequireDatasetDelete` |
| GetDataset | GET /api/v2/datasets/{dataset_id} | – | dto.DatasetDetailResp | `producer.GetDatasetDetail` | BA + `RequireDatasetRead` |
| ListDatasets | GET /api/v2/datasets | dto.ListDatasetReq | dto.ListResp | `producer.ListDatasets` | BA + `RequireDatasetRead` |
| SearchDataset | POST /api/v2/datasets/search | dto.SearchDatasetReq | dto.ListResp | `producer.SearchDatasets` | BA + `RequireDatasetRead` |
| UpdateDataset | PATCH /api/v2/datasets/{dataset_id} | dto.UpdateDatasetReq | dto.DatasetDetailResp | `producer.UpdateDataset` | BA + `RequireDatasetUpdate` |
| ManageDatasetCustomLabels | PATCH /api/v2/datasets/{dataset_id}/labels | dto.ManageLabelsReq | – | `producer.ManageDatasetLabels` | BA + `RequireDatasetUpdate` |
| CreateDatasetVersion | POST /api/v2/datasets/{dataset_id}/versions | dto.CreateDatasetVersionReq | dto.DatasetVersionDetailResp | `producer.CreateDatasetVersion` | BA + `RequireDatasetVersionCreate` |
| DeleteDatasetVersion | DELETE /api/v2/datasets/{dataset_id}/versions/{version_id} | – | – | `producer.DeleteDatasetVersion` | BA + `RequireDatasetVersionDelete` |
| GetDatasetVersion | GET /api/v2/datasets/{dataset_id}/versions/{version_id} | – | dto.DatasetVersionDetailResp | `producer.GetDatasetVersionDetail` | BA + `RequireDatasetVersionRead` |
| ListDatasetVersions | GET /api/v2/datasets/{dataset_id}/versions | dto.ListDatasetVersionReq | dto.ListResp | `producer.ListDatasetVersions` | BA + `RequireDatasetVersionRead` |
| UpdateDatasetVersion | PATCH /api/v2/datasets/{dataset_id}/versions/{version_id} | dto.UpdateDatasetVersionReq | dto.DatasetVersionDetailResp | `producer.UpdateDatasetVersion` | BA + `RequireDatasetVersionUpdate` |
| DownloadDatasetVersion | GET /api/v2/datasets/{dataset_id}/versions/{version_id}/download | – | zip stream | `producer.GetDatasetVersionFilename`, `producer.DownloadDatasetVersion` | BA + `RequireDatasetVersionDownload` |
| ManageDatasetVersionInjections | PATCH /api/v2/datasets/{dataset_id}/version/{version_id}/injections | dto.ManageDatasetVersionInjectionsReq | – | `producer.ManageDatasetVersionInjections` | BA + `RequireDatasetVersionUpdate` |

### handlers/v2/containers.go
| Func | Verb + Route | Req | Resp | Service | Auth |
|---|---|---|---|---|---|
| CreateContainer | POST /api/v2/containers | dto.CreateContainerReq | dto.ContainerDetailResp | `producer.CreateContainer` | BA + `RequireContainerCreate` |
| DeleteContainer | DELETE /api/v2/containers/{container_id} | – | – | `producer.DeleteContainer` | BA + `RequireContainerDelete` |
| GetContainer | GET /api/v2/containers/{container_id} | – | dto.ContainerDetailResp | `producer.GetContainerDetail` | BA + `RequireContainerRead` |
| ListContainers | GET /api/v2/containers | dto.ListContainerReq | dto.ListResp | `producer.ListContainers` | BA + `RequireContainerRead` |
| UpdateContainer | PATCH /api/v2/containers/{container_id} | dto.UpdateContainerReq | dto.ContainerDetailResp | `producer.UpdateContainer` | BA + `RequireContainerUpdate` |
| CreateContainerVersion | POST /api/v2/containers/{container_id}/versions | dto.CreateContainerVersionReq | dto.ContainerVersionDetailResp | `producer.CreateContainerVersion` | BA + `RequireContainerVersionCreate` |
| DeleteContainerVersion | DELETE /api/v2/containers/{container_id}/versions/{version_id} | – | – | `producer.DeleteContainerVersion` | BA + `RequireContainerVersionDelete` |
| GetContainerVersion | GET /api/v2/containers/{container_id}/versions/{version_id} | – | dto.ContainerVersionDetailResp | `producer.GetContainerVersionDetail` | BA + `RequireContainerVersionRead` |
| ListContainerVersions | GET /api/v2/containers/{container_id}/versions | dto.ListContainerVersionReq | dto.ListResp | `producer.ListContainerVersions` | BA + `RequireContainerVersionRead` |
| UpdateContainerVersion | PATCH /api/v2/containers/{container_id}/versions/{version_id} | dto.UpdateContainerVersionReq | dto.ContainerVersionDetailResp | `producer.UpdateContainerVersion` | BA + `RequireContainerVersionUpdate` |
| SetContainerVersionImage | PATCH /api/v2/container-versions/{id}/image | dto.SetContainerVersionImageReq | dto.ContainerVersionDetailResp | `producer.SetContainerVersionImage` | BA + `RequireContainerVersionUpdate` |
| ManageContainerCustomLabels | PATCH /api/v2/containers/{container_id}/labels | dto.ManageLabelsReq | – | `producer.ManageContainerLabels` | BA + `RequireContainerUpdate` |
| SubmitContainerBuilding | POST /api/v2/containers/build | dto.SubmitContainerBuildingReq | dto.SubmitContainerBuildingResp | `producer.ProduceContainerBuildingTask` | BA + `RequireContainerExecute` |
| UploadHelmChart | POST /api/v2/containers/{container_id}/versions/{version_id}/helm-chart | multipart | dto.UploadHelmChartResp | `producer.UploadHelmChart` | BA + `RequireContainerVersionUpload` |
| UploadHelmValueFile | POST /api/v2/containers/{container_id}/versions/{version_id}/helm-values | multipart | dto.UploadHelmValueFileResp | `producer.UploadHelmValueFile` | BA + `RequireContainerVersionUpload` |

### handlers/v2/labels.go, permissions.go, resources.go, roles.go
| Func | Verb + Route | Req | Resp | Service | Auth |
|---|---|---|---|---|---|
| BatchDeleteLabels | POST /api/v2/labels/batch-delete | dto.BatchDeleteLabelReq | – | `producer.BatchDeleteLabels` | BA + `RequireLabelDelete` |
| CreateLabel | POST /api/v2/labels | dto.CreateLabelReq | dto.LabelResp | `producer.CreateLabel` | BA + `RequireLabelCreate` |
| DeleteLabel | DELETE /api/v2/labels/{label_id} | – | – | `producer.DeleteLabel` | BA + `RequireLabelDelete` |
| GetLabelDetail | GET /api/v2/labels/{label_id} | – | dto.LabelResp | `producer.GetLabelDetail` | BA + `RequireLabelRead` |
| ListLabels | GET /api/v2/labels | dto.ListLabelReq | dto.ListResp | `producer.ListLabels` | BA + `RequireLabelRead` |
| UpdateLabel | PATCH /api/v2/labels/{label_id} | dto.UpdateLabelReq | dto.LabelResp | `producer.UpdateLabel` | BA + `RequireLabelUpdate` |
| GetPermission | GET /api/v2/permissions/{id} | – | dto.PermissionResp | `producer.GetPermissionDetail` | BA + `RequirePermissionRead` |
| ListPermissions | GET /api/v2/permissions | dto.ListPermissionReq | dto.ListResp | `producer.ListPermissions` | BA + `RequirePermissionRead` |
| ListRolesFromPermission | GET /api/v2/permissions/{permission_id}/roles | – | []dto.RoleResp | `producer.ListRolesFromPermission` | BA + `RequirePermissionRead` |
| GetResourceDetail | GET /api/v2/resources/{id} | – | dto.ResourceDetailResp | `producer.GetResourceDetail` | BA |
| ListResources | GET /api/v2/resources | dto.ListResourceReq | dto.ListResp | `producer.ListResources` | BA |
| ListResourcePermissions | GET /api/v2/resources/{id}/permissions | – | []dto.PermissionResp | `producer.ListResourcePermissions` | BA |
| CreateRole | POST /api/v2/roles | dto.CreateRoleReq | dto.RoleResp | `producer.CreateRole` | BA + `RequireRoleCreate` |
| DeleteRole | DELETE /api/v2/roles/{id} | – | – | `producer.DeleteRole` | BA + `RequireRoleDelete` |
| GetRole | GET /api/v2/roles/{id} | – | dto.RoleResp | `producer.GetRoleDetail` | BA + `RequireRoleRead` |
| ListRoles | GET /api/v2/roles | dto.ListRoleReq | dto.ListResp | `producer.ListRoles` | BA + `RequireRoleRead` |
| UpdateRole | PATCH /api/v2/roles/{id} | dto.UpdateRoleReq | dto.RoleResp | `producer.UpdateRole` | BA + `RequireRoleUpdate` |
| AssignRolePermission | POST /api/v2/roles/{role_id}/permissions/assign | dto.BatchAssignRolePermissionsReq | – | `producer.BatchAssignRolePermissions` | BA + `RequireRoleGrant` |
| RemovePermissionsFromRole | POST /api/v2/roles/{role_id}/permissions/remove | dto.BatchRemoveRolePermissionsReq | – | `producer.RemovePermissionsFromRole` | BA + `RequireRoleRevoke` |

### handlers/v2/groups.go, traces.go, notifications.go, tasks.go (streaming + queue)
| Func | Verb + Route | Req | Resp | Service | Auth |
|---|---|---|---|---|---|
| GetGroupStats | GET /api/v2/groups/{group_id}/stats | dto.GroupStatsReq (path) | dto.GroupStatsResp | `producer.GetGroupStats` | BA |
| GetGroupStream | GET /api/v2/groups/{group_id}/stream | dto.GroupStreamReq (query) | SSE event stream | `producer.NewGroupStreamProcessor`, `producer.ReadGroupStreamMessages` | BA |
| GetTrace | GET /api/v2/traces/{trace_id} | – | dto.TraceDetailResp | `producer.GetTraceDetail` | BA + `RequireTraceRead` |
| ListTraces | GET /api/v2/traces | dto.ListTraceReq | dto.ListResp | `producer.ListTraces` | BA + `RequireTraceRead` |
| GetTraceStream | GET /api/v2/traces/{trace_id}/stream | dto.TraceStreamReq | SSE event stream | `producer.GetTraceStreamProcessor`, `producer.ReadTraceStreamMessages` | BA + `RequireTraceMonitor` |
| GetNotificationStream | GET /api/v2/notifications/stream | dto.NotificationStreamReq | SSE event stream | `producer.ReadNotificationStreamMessages` | BA |
| BatchDeleteTasks | POST /api/v2/tasks/batch-delete | dto.BatchDeleteTaskReq | – | `producer.BatchDeleteTasks` | BA + `RequireTaskDelete` |
| GetTask | GET /api/v2/tasks/{task_id} | – | dto.TaskDetailResp | `producer.GetTaskDetail` | BA + `RequireTaskRead` |
| ListTasks | GET /api/v2/tasks | dto.ListTaskReq | dto.ListResp[TaskResp] | `producer.ListTasks` | BA + `RequireTaskRead` |
| ExpediteTask | POST /api/v2/tasks/{task_id}/expedite | – | dto.TaskResp | `producer.ExpediteTask` | BA + `RequireTaskExecute` |
| GetTaskLogsWS | GET /api/v2/tasks/{task_id}/logs/ws | query token | WebSocket | `utils.ValidateToken`, `repository.GetTaskByID` (direct), `producer.NewTaskLogStreamer` | token via ?token= query |

### handlers/v2/evaluations.go, sdk_evaluations.go, metrics.go, system.go, systems.go, rate_limiters.go, pedestal_helm.go
| Func | Verb + Route | Req | Resp | Service | Auth |
|---|---|---|---|---|---|
| ListDatapackEvaluationResults | POST /api/v2/evaluations/datapacks | dto.ListDatapackEvalReq | dto.DatapackEvalResp | `analyzer.ListDatapackEvaluationResults` | BA |
| ListDatasetEvaluationResults | POST /api/v2/evaluations/datasets | dto.ListDatasetEvalReq | dto.DatasetEvalResp | `analyzer.ListDatasetEvaluationResults` | BA |
| ListEvaluations | GET /api/v2/evaluations | dto.ListEvaluationReq | dto.ListResp | `producer.ListEvaluations` | BA |
| GetEvaluation | GET /api/v2/evaluations/{id} | – | dto.EvaluationDetailResp | `producer.GetEvaluation` | BA |
| DeleteEvaluation | DELETE /api/v2/evaluations/{id} | – | – | `producer.DeleteEvaluation` | BA |
| ListSDKEvaluations | GET /api/v2/sdk/evaluations | dto.ListSDKEvaluationReq | dto.ListResp[database.SDKEvaluationSample] | `producer.ListSDKEvaluations` | BA |
| GetSDKEvaluation | GET /api/v2/sdk/evaluations/{id} | – | database.SDKEvaluationSample | `producer.GetSDKEvaluation` | BA |
| ListSDKExperiments | GET /api/v2/sdk/evaluations/experiments | – | dto.SDKExperimentListResp | `producer.ListSDKExperiments` | BA |
| ListSDKDatasetSamples | GET /api/v2/sdk/datasets | dto.ListSDKDatasetSampleReq | dto.ListResp[database.SDKDatasetSample] | `producer.ListSDKDatasetSamples` | BA |
| GetInjectionMetrics | GET /api/v2/metrics/injections | dto.InjectionMetricsReq | dto.InjectionMetricsResp | `producer.GetInjectionMetrics` | BA |
| GetExecutionMetrics | GET /api/v2/metrics/executions | dto.ExecutionMetricsReq | dto.ExecutionMetricsResp | `producer.GetExecutionMetrics` | BA |
| GetAlgorithmMetrics | GET /api/v2/metrics/algorithms | dto.AlgorithmMetricsReq | dto.AlgorithmMetricsResp | `producer.GetAlgorithmMetrics` | BA |
| GetSystemMetrics | GET /api/v2/system/metrics | – | dto.SystemMetricsResp | `producer.GetSystemMetrics(ctx)` | BA |
| GetSystemMetricsHistory | GET /api/v2/system/metrics/history | – | dto.SystemMetricsHistoryResp | `producer.GetSystemMetricsHistory(ctx)` | BA |
| ListChaosSystemsHandler | GET /api/v2/systems | dto.ListChaosSystemReq | dto.ListResp | `producer.ListChaosSystemsService` | BA |
| GetChaosSystemHandler | GET /api/v2/systems/{id} | – | dto.ChaosSystemDetailResp | `producer.GetChaosSystemService` | BA |
| CreateChaosSystemHandler | POST /api/v2/systems | dto.CreateChaosSystemReq | dto.ChaosSystemResp | `producer.CreateChaosSystemService` | BA |
| UpdateChaosSystemHandler | PUT /api/v2/systems/{id} | dto.UpdateChaosSystemReq | dto.ChaosSystemResp | `producer.UpdateChaosSystemService` | BA |
| DeleteChaosSystemHandler | DELETE /api/v2/systems/{id} | – | – | `producer.DeleteChaosSystemService` | BA |
| UpsertChaosSystemMetadataHandler | POST /api/v2/systems/{id}/metadata | dto.UpsertChaosSystemMetadataReq | – | `producer.UpsertChaosSystemMetadataService` | BA |
| ListChaosSystemMetadataHandler | GET /api/v2/systems/{id}/metadata | – | []dto.ChaosSystemMetadataResp | `producer.ListChaosSystemMetadataService` | BA |
| ListRateLimiters | GET /api/v2/rate-limiters | – | dto.ListRateLimitersResp | `producer.ListRateLimiters(ctx)` | BA |
| ResetRateLimiter | DELETE /api/v2/rate-limiters/{bucket} | – | – | `producer.ResetRateLimiter(ctx, bucket)` | BA |
| GCRateLimiters | POST /api/v2/rate-limiters/gc | – | dto.GCRateLimitersResp | `producer.GCRateLimiters(ctx)` | BA |
| GetPedestalHelmConfig | GET /api/v2/pedestal/helm/{container_version_id} | – | dto.PedestalHelmConfigResp | **direct repo**: `repository.GetHelmConfigByContainerVersionID(database.DB,…)` | BA |
| UpsertPedestalHelmConfig | PUT /api/v2/pedestal/helm/{container_version_id} | dto.UpsertPedestalHelmConfigReq | dto.PedestalHelmConfigResp | **direct repo**: `repository.GetHelmConfigByContainerVersionID`, `repository.UpdateHelmConfig`, `repository.BatchCreateHelmConfigs` | BA |
| VerifyPedestalHelmConfig | POST /api/v2/pedestal/helm/{container_version_id}/verify | – | dto.PedestalHelmVerifyResp | **direct repo** + `pedestalhelm.Run` (helm shell-out) | BA |

### handlers/system/* and handlers/* root
| Func | Verb + Route | Req | Resp | Service | Auth |
|---|---|---|---|---|---|
| GetAuditLog | GET /system/audit/{id} | – | dto.AuditLogDetailResp | `producer.GetAuditLogDetail` | BA + `RequireAuditRead` |
| ListAuditLogs | GET /system/audit | dto.ListAuditLogReq | dto.ListResp[AuditLogResp] | `producer.ListAuditLogs` | BA + `RequireAuditRead` |
| GetConfig | GET /system/configs/{config_id} | – | dto.ConfigDetailResp | `producer.GetConfigDetail` | BA + `RequireConfigurationRead` |
| ListConfigs | GET /system/configs | dto.ListConfigReq | dto.ListResp | `producer.ListConfigs` | BA + `RequireConfigurationRead` |
| RollbackConfigValue | POST /system/configs/{config_id}/value/rollback | dto.RollbackConfigValueReq | – | `producer.RollbackConfigValue(ctx,…,user,IP,UA)` | BA + `RequireConfigurationUpdate` |
| RollbackConfigMetadata | POST /system/configs/{config_id}/metadata/rollback | dto.RollbackConfigMetadataReq | dto.ConfigDetailResp | `producer.RollbackConfigMetadata` | BA + `RequireConfigurationConfigure` |
| UpdateConfigValue | PATCH /system/configs/{config_id} | dto.UpdateConfigValueReq | – | `producer.UpdateConfigValue` | BA + `RequireConfigurationUpdate` |
| UpdateConfigMetadata | PUT /system/configs/{config_id}/metadata | dto.UpdateConfigMetadataReq | dto.ConfigDetailResp | `producer.UpdateConfigMetadata` | BA + `RequireConfigurationConfigure` |
| ListConfigHistories | GET /system/configs/{config_id}/histories | dto.ListConfigHistoryReq | dto.ListResp | `producer.ListConfigHistories` | BA + `RequireConfigurationRead` |
| GetHealth | GET /system/health | – | dto.HealthCheckResp | direct: `database.DB.Raw("SELECT 1")`, `k8s.GetK8sClient`, `client.GetRedisClient`, http to BuildKit/Jaeger | none |
| GetMetrics | POST /system/monitor/metrics | dto.MonitoringQueryReq | dto.MonitoringMetricsResp | hardcoded data (`@Deprecated`) | BA |
| GetSystemInfo | GET /system/monitor/info | – | dto.SystemInfoResp | `runtime.*` only | BA |
| ListNamespaceLocks | GET /system/monitor/namespaces/locks | – | dto.NamespaceLocksResp | `producer.InspectLock(ctx)` | BA |
| ListQueuedTasks | POST /system/monitor/tasks/queue | – | dto.QueuedTasksResp | `producer.ListQueuedTasks(ctx)` | BA |
| SwaggerModelsDoc | GET /api/_docs/models | – | consts.SSEEventName | empty body — type-anchor for swagger | none |
| GetAllVars | – (no Router; debug mux) | – | map | `debug.NewDebugRegistry().GetAll()` | mounted under debug only |
| GetVar | – (no Router) | dto.DebugGetReq | any | `debug.NewDebugRegistry().Get(name)` | – |
| SetVar | – (no Router) | dto.DebugSetReq | – | `debug.NewDebugRegistry().Set(name,val)` | – |

---

## 3. Middleware chain (per file)

| File:Line | Exported name | Checks | Sets on `gin.Context` | Failure mode |
|---|---|---|---|---|
| middleware.go:19 | `SSEPath()` | URL matches `^/stream(/.*)?$` | response headers (Content-Type=text/event-stream, no-cache, keep-alive, chunked) | none — pass-through |
| middleware.go:34 | `GroupID()` | request method == POST | `groupID` (uuid string), header `X-Group-ID` | none |
| middleware.go:46 | `TracerMiddleware()` | starts an OTel span "rcabench/group" using `groupID` | `otel-span-context` (=ctx) | span.End() in defer; never aborts |
| auth.go:14 | `JWTAuth()` | extracts Bearer; tries `utils.ValidateToken` then `utils.ValidateServiceToken` | user token: `user_id`,`username`,`email`,`is_active`,`is_admin`,`user_roles`,`token_expires_at`,`token_type`="user". service token: `task_id`,`token_expires_at`,`token_type`="service",`is_service_token`=true | 401 + Abort if both validations fail |
| auth.go:63 | `OptionalJWTAuth()` | same as JWTAuth but missing/invalid token = pass-through | same keys when present | never aborts |
| auth.go:248 | `RequireActiveUser()` | calls `RequireAuth(c)` (must be authed user OR service token; if user, must be active) | – | 401 (auth required), 403 (inactive) |
| permission.go:328 | `RequirePermission(rule)` | `producer.CheckUserPermission` against rule | – | 401 (no auth), 403 (deny), 500 (DB err) |
| permission.go:333 | `RequireAnyPermission([]rule)` | OR over rules via `producer.CheckUserPermission` | – | same |
| permission.go:338 | `RequireAllPermissions([]rule)` | AND over rules | – | same |
| permission.go:344 | `RequireOwnership(type,getter)` | `ctx.userID == *ownerID` from supplied getter | – | 403 / 500 |
| permission.go:350 | `RequireAdminOrOwnership(type,getter)` | JWT `is_admin` || ownership | – | same |
| permission.go:355 | `RequireSystemAdmin()` | JWT `is_admin` only | – | 403 |
| permission.go:364 | `RequireTeamAccess(requireAdmin bool)` | reads URL param `team_id`; if admin = `producer.IsUserTeamAdmin`; else `producer.IsUserInTeam` OR `producer.IsTeamPublic` | – | 403 / 500 |
| permission.go:371 | `RequireProjectAccess(requireAdmin bool)` | URL `project_id`; admin = `producer.IsUserProjectAdmin`; else `producer.IsUserInProject` | – | 403 / 500 |
| permission.go:381–497 | Pre-built vars: `RequireSystemRead/Configure`, `RequireAuditRead/Audit`, `RequireConfigurationRead/Update/Configure`, `RequireUserOwnership`, `RequireAdminOrUserOwnership`, `RequireTeamAdminAccess`, `RequireTeamMemberAccess`, `RequireProjectAdminAccess`, `RequireProjectMemberAccess`, `RequireUserRead/Create/Update/Delete/Assign`, `RequireRoleRead/Create/Update/Delete/Grant/Revoke`, `RequirePermissionRead`, `RequireTeamRead/Create/Update/Delete/Manage`, `RequireProjectRead/Create/Update/Delete/Manage`, `RequireProjectInjectionExecute`, `RequireProjectExecutionExecute`, `RequireContainerRead/Create/Update/Delete/Manage/Execute`, `RequireContainerVersionRead/Create/Update/Delete/Manage/Upload`, `RequireDatasetRead/Create/Update/Delete/Manage`, `RequireDatasetVersionRead/Create/Update/Delete/Manage/Download`, `RequireLabelRead/Create/Update/Delete`, `RequireTaskRead/Create/Update/Delete/Execute/Stop`, `RequireTraceRead/Monitor` | composed from the above | – | 401/403/500 |
| audit.go:23 | `AuditMiddleware()` | post-flight audit; skips GET + paths in {`/system/health`,`/system/monitor/metrics`,`/system/monitor/namespace-locks`}; redacts password/token/secret/api_key/apiKey | – (writes to `c.Writer` via `bodyLogWriter`) | async log; never aborts. On status>=400 calls `producer.LogFailedAction`, else `producer.LogUserAction` |
| ratelimit.go:115 | `RateLimit(limiter,keyFunc)` | in-memory sliding window | – | 429 + Abort |
| ratelimit.go:130 | `IPBasedRateLimit(limiter)` | key = X-Forwarded-For/X-Real-IP/ClientIP | – | 429 |
| ratelimit.go:144 | `UserBasedRateLimit(limiter)` | key = `user_<id>` else IP | – | 429 |
| ratelimit.go:163 | `APIKeyBasedRateLimit(limiter)` | key = `api_<X-API-Key>` else IP | – | 429 |
| ratelimit.go:177 vars | `GeneralRateLimit` (1000/min IP), `AuthRateLimit` (100/min IP), `StrictRateLimit` (20/min user), `UserRateLimit` (1000/min user) | – | – | 429 |
| ratelimit.go:101 | `StartCleanupRoutine()` | – (background ticker, 5min) | – | – |

`bodyLogWriter` (audit.go:113) wraps `gin.ResponseWriter` to capture response body.

Helper getters in auth.go: `GetCurrentUserID`, `GetCurrentUsername`, `GetCurrentUserEmail`, `IsCurrentUserActive`, `IsServiceToken`, `GetTokenType`, `IsCurrentUserAdmin`, `GetCurrentUserRoles`, `GetServiceTaskID`, `RequireAuth`, `RequireUserAuth`.

---

## 4. Handler → service dependency edge list (deduped)

`producer.*` (default package alias `producer "aegis/service/producer"` everywhere except `injections.go`/`projects.go` which use `service/producer` direct):

```
producer.AddTeamMember
producer.AssignContainerToUser
producer.AssignDatasetToUser
producer.AssignProjectToUser
producer.AssignRoleToUser
producer.BatchAssignRolePermissions
producer.BatchAssignUserPermissions
producer.BatchCreateDetectorResults
producer.BatchCreateGranularityResults
producer.BatchDeleteExecutionsByIDs
producer.BatchDeleteExecutionsByLabels
producer.BatchDeleteInjectionsByIDs
producer.BatchDeleteInjectionsByLabels
producer.BatchDeleteLabels
producer.BatchDeleteTasks
producer.BatchManageInjectionLabels
producer.BatchRemoveUserPermissions
producer.ChangePassword
producer.CheckUserPermission                    // middleware/permission.go
producer.CloneInjection
producer.CreateChaosSystemService
producer.CreateContainer
producer.CreateContainerVersion
producer.CreateDataset
producer.CreateDatasetVersion
producer.CreateLabel
producer.CreateProject
producer.CreateRole
producer.CreateTeam
producer.CreateUser
producer.DeleteChaosSystemService
producer.DeleteContainer
producer.DeleteContainerVersion
producer.DeleteDataset
producer.DeleteDatasetVersion
producer.DeleteEvaluation
producer.DeleteLabel
producer.DeleteProject
producer.DeleteRole
producer.DeleteTeam
producer.DeleteUser
producer.DownloadDatapack
producer.DownloadDatapackFile
producer.DownloadDatasetVersion
producer.ExpediteTask
producer.FriendlySpecToNode                     // injections.go:866
producer.GCRateLimiters
producer.GetAlgorithmMetrics
producer.GetAuditLogDetail
producer.GetChaosSystemService
producer.GetConfigDetail
producer.GetContainerDetail
producer.GetContainerVersionDetail
producer.GetDatapackFilename
producer.GetDatapackFiles
producer.GetDatasetDetail
producer.GetDatasetVersionDetail
producer.GetDatasetVersionFilename
producer.GetEvaluation
producer.GetExecutionDetail
producer.GetExecutionMetrics
producer.GetGroupStats
producer.GetInjectionDetail
producer.GetInjectionLogs
producer.GetInjectionMetrics
producer.GetLabelDetail
producer.GetPermissionDetail
producer.GetProfile
producer.GetProjectDetail
producer.GetResourceDetail
producer.GetRoleDetail
producer.GetSDKEvaluation
producer.GetSystemMetrics
producer.GetSystemMetricsHistory
producer.GetTaskDetail
producer.GetTeamDetail
producer.GetTraceDetail
producer.GetTraceStreamProcessor
producer.GetUserDetail
producer.InspectLock                            // system/monitor.go
producer.IsTeamPublic                           // middleware/permission.go
producer.IsUserInProject                        // middleware/permission.go
producer.IsUserInTeam                           // middleware/permission.go
producer.IsUserProjectAdmin                     // middleware/permission.go
producer.IsUserTeamAdmin                        // middleware/permission.go
producer.ListAuditLogs
producer.ListAvaliableExecutionLabels
producer.ListChaosSystemMetadataService
producer.ListChaosSystemsService
producer.ListConfigHistories
producer.ListConfigs
producer.ListContainerVersions
producer.ListContainers
producer.ListDatasetVersions
producer.ListDatasets
producer.ListEvaluations
producer.ListExecutions
producer.ListInjections
producer.ListInjectionsNoIssues
producer.ListInjectionsWithIssues
producer.ListLabels
producer.ListPermissions
producer.ListProjectExecutions
producer.ListProjectInjections
producer.ListProjects
producer.ListQueuedTasks                        // system/monitor.go
producer.ListRateLimiters
producer.ListResourcePermissions
producer.ListResources
producer.ListRoles
producer.ListRolesFromPermission
producer.ListSDKDatasetSamples
producer.ListSDKEvaluations
producer.ListSDKExperiments
producer.ListTaskMembers? -> producer.ListTeamMembers
producer.ListTasks
producer.ListTeamMembers
producer.ListTeamProjects
producer.ListTeams
producer.ListTraces
producer.ListUsers
producer.ListUsersFromRole
producer.LogFailedAction                        // middleware/audit.go
producer.LogUserAction                          // middleware/audit.go
producer.Login
producer.Logout
producer.ManageContainerLabels
producer.ManageDatasetLabels
producer.ManageDatasetVersionInjections
producer.ManageExecutionLabels
producer.ManageInjectionLabels
producer.ManageProjectLabels
producer.NewGroupStreamProcessor
producer.NewTaskLogStreamer
producer.ProduceAlgorithmExeuctionTasks
producer.ProduceContainerBuildingTask
producer.ProduceDatapackBuildingTasks
producer.ProduceRestartPedestalTasks
producer.QueryDatapackFileContent
producer.ReadGroupStreamMessages
producer.ReadNotificationStreamMessages
producer.ReadTraceStreamMessages
producer.RefreshToken
producer.Register
producer.RemoveContainerFromUser
producer.RemoveDatasetFromUser
producer.RemovePermissionsFromRole
producer.RemoveProjectFromUser
producer.RemoveRoleFromUser
producer.RemoveTeamMember
producer.ResetRateLimiter
producer.RollbackConfigMetadata
producer.RollbackConfigValue
producer.SearchDatasets
producer.SearchInjections
producer.SetContainerVersionImage
producer.UpdateChaosSystemService
producer.UpdateConfigMetadata
producer.UpdateConfigValue
producer.UpdateContainer
producer.UpdateContainerVersion
producer.UpdateDataset
producer.UpdateDatasetVersion
producer.UpdateGroundtruth
producer.UpdateLabel
producer.UpdateProject
producer.UpdateRole
producer.UpdateTeam
producer.UpdateTeamMemberRole
producer.UpdateUser
producer.UploadDatapack
producer.UploadHelmChart
producer.UploadHelmValueFile
producer.UpsertChaosSystemMetadataService
```

`analyzer.*` (alias `service/analyzer`):
```
analyzer.ListDatapackEvaluationResults          // evaluations.go:51
analyzer.ListDatasetEvaluationResults           // evaluations.go:94
```

`consumer.*` and `common.*`: **no direct calls from handlers/middleware** (verified via Grep `(consumer|common)\.\w+\(` in `handlers/`).

---

## 5. Handler → repository/database direct calls (bypassing service)

| File:Line | Symbol |
|---|---|
| handlers/v2/tasks.go:208 | `repository.GetTaskByID(database.DB, taskID)` (used to verify task before WS upgrade) |
| handlers/v2/pedestal_helm.go:46 | `repository.GetHelmConfigByContainerVersionID(database.DB, versionID)` |
| handlers/v2/pedestal_helm.go:91 | `repository.GetHelmConfigByContainerVersionID` |
| handlers/v2/pedestal_helm.go:104 | `repository.UpdateHelmConfig(database.DB, existing)` |
| handlers/v2/pedestal_helm.go:121 | `repository.BatchCreateHelmConfigs(database.DB, []*database.HelmConfig{…})` |
| handlers/v2/pedestal_helm.go:151 | `repository.GetHelmConfigByContainerVersionID` |
| handlers/v2/pedestal_helm.go:112 | direct construction of `database.HelmConfig{…}` entity |
| handlers/system/health.go:108,122 | `database.DB == nil` and `database.DB.WithContext(ctx).Raw("SELECT 1").Scan(...)` |

Handler-level imports `aegis/repository` only in: `handlers/v2/tasks.go`, `handlers/v2/pedestal_helm.go`. Imports `aegis/database` in: `handlers/v2/tasks.go`, `handlers/v2/pedestal_helm.go`, `handlers/system/health.go`.

---

## 6. Long-lived endpoints (SSE / WS / stream)

| Handler | Path | Mechanism | Event vocabulary |
|---|---|---|---|
| `v2.GetGroupStream` (groups.go:76) | GET `/api/v2/groups/{group_id}/stream` | SSE via `gin-contrib/sse` + `c.Render(-1, sse.Event{...})`; reads from `producer.NewGroupStreamProcessor` + Redis XStream replay & live tail | `consts.EventEnd` terminator + arbitrary stream events with `id`, `event`, `data` from group stream processor (groups.go:181 `sendGroupSSEEvents`) |
| `v2.GetTraceStream` (traces.go:113) | GET `/api/v2/traces/{trace_id}/stream` | SSE via `gin-contrib/sse`; replays history then tails | `consts.EventEnd` + custom events emitted from `producer.StreamProcessor.ProcessMessageForSSE` (traces.go:226) |
| `v2.GetNotificationStream` (notifications.go:34) | GET `/api/v2/notifications/stream` | SSE; reads from notification XStream | events shaped by `sendNotificationSSEEvents` (notifications.go:114) |
| `v2.GetTaskLogsWS` (tasks.go:188) | GET `/api/v2/tasks/{task_id}/logs/ws` | WebSocket via `gorilla/websocket` (`wsUpgrader`, tasks.go:20). Auth via `?token=` query (WS can't carry custom headers). Delegates to `producer.NewTaskLogStreamer.StreamLogs(ctx, task)` | `dto.WSLogMessage` frames |
| `middleware.SSEPath` (middleware.go:19) | any URL matching `^/stream(/.*)?$` | sets SSE headers if path matches | – |

---

## 7. SDK-exposed handlers (`@x-api-type {"sdk":"true"}`)

A handler is included in the generated SDK iff its swagger block carries this tag. Below is every handler with that tag (deduped from grep). 80 endpoints.

`handlers/docs.go` — `SwaggerModelsDoc` (api/_docs/models)

`handlers/system/` — `GetHealth`

`handlers/v2/auth.go` — `Register`, `Login`

`handlers/v2/users.go` — `CreateUser`, `DeleteUser`, `GetUserDetailV2`, `ListUsersV2`, `UpdateUser`

`handlers/v2/permissions.go` — `ListRolesFromPermission`

`handlers/v2/resources.go` — `GetResourceDetail`, `ListResources`, `ListResourcePermissions`

`handlers/v2/system.go` — `GetSystemMetrics`, `GetSystemMetricsHistory`

`handlers/v2/groups.go` — `GetGroupStats`, `GetGroupStream` (also `@x-request-type {"stream":"true"}`)

`handlers/v2/metrics.go` — `GetInjectionMetrics`, `GetExecutionMetrics`, `GetAlgorithmMetrics`

`handlers/v2/evaluations.go` — `ListDatapackEvaluationResults`, `ListDatasetEvaluationResults`, `ListEvaluations`, `GetEvaluation`, `DeleteEvaluation`

`handlers/v2/teams.go` — `CreateTeam`, `GetTeamDetail`, `ListTeams`, `ListTeamProjects`, `ListTeamMembers`

`handlers/v2/containers.go` — `CreateContainer`, `GetContainer`, `ListContainers`, `CreateContainerVersion`, `GetContainerVersion`, `ListContainerVersions`, `SetContainerVersionImage`, `SubmitContainerBuilding`

`handlers/v2/labels.go` — `BatchDeleteLabels`, `CreateLabel`, `DeleteLabel`, `GetLabelDetail`, `ListLabels`, `UpdateLabel`

`handlers/v2/injections.go` — `GetInjection`, `GetInjectionMetadata`, `ListInjections`, `SearchInjections`, `ListFaultInjectionNoIssues`, `ListFaultInjectionWithIssues`, `ManageInjectionCustomLabels`, `BatchManageInjectionLabels`, `SubmitFaultInjection`, `SubmitDatapackBuilding`, `CloneInjection`, `GetInjectionLogs`, `DownloadDatapack`

`handlers/v2/datasets.go` — `CreateDataset`, `GetDataset`, `ListDatasets`, `SearchDataset`, `CreateDatasetVersion`, `GetDatasetVersion`, `ListDatasetVersions`, `DownloadDatasetVersion`, `ManageDatasetVersionInjections`

`handlers/v2/traces.go` — `GetTrace`, `ListTraces`, `GetTraceStream`

`handlers/v2/pedestal_helm.go` — `GetPedestalHelmConfig`, `UpsertPedestalHelmConfig`, `VerifyPedestalHelmConfig`

`handlers/v2/rate_limiters.go` — `ListRateLimiters`, `ResetRateLimiter`, `GCRateLimiters`

`handlers/v2/roles.go` — `CreateRole`, `DeleteRole`, `GetRole`, `ListRoles`, `UpdateRole`, `AssignRolePermission`, `RemovePermissionsFromRole`

`handlers/v2/projects.go` — `CreateProject`, `GetProjectDetail`, `ListProjects`, `ListProjectInjections`, `SearchProjectInjections`, `ListProjectFaultInjectionNoIssues`, `ListProjectFaultInjectionWithIssues`, `SubmitProjectFaultInjection`, `SubmitProjectDatapackBuilding`, `ListProjectExecutions`

`handlers/v2/executions.go` — `GetExecution`, `ListExecutions`, `ListAvaliableExecutionLabels`, `SubmitAlgorithmExecution`, `UploadDetectorResults`, `UploadGranularityResults`

`handlers/v2/tasks.go` — `GetTask`, `ExpediteTask` (note: `BatchDeleteTasks`, `ListTasks`, `GetTaskLogsWS` are **not** SDK-exposed)

NOT SDK-exposed but live: most teams/* member mutations (`AddTeamMember`, `RemoveTeamMember`, `UpdateTeamMemberRole`, `UpdateTeam`, `DeleteTeam`), users/* role/perm/resource assignments, `UploadDatapack`, `UpdateGroundtruth`, `TranslateFaultSpecs` (410), `GetSystemMapping`, `UpdateProject`, `DeleteProject`, `ManageProjectCustomLabels`, container `UploadHelmChart`/`UploadHelmValueFile`/`UpdateContainer*`/`DeleteContainer*`/`ManageContainerCustomLabels`, `UpdateDataset`, `DeleteDataset`, `ManageDatasetCustomLabels`, `UpdateDatasetVersion`, `DeleteDatasetVersion`, dataset `ManageDatasetCustomLabels`, dataset injection `ManageExecutionCustomLabels`, all `systems.go` handlers, all `permissions.go` except `ListRolesFromPermission`, `auth.go`'s `RefreshToken`/`Logout`/`ChangePassword`/`GetProfile`, system handlers except `GetHealth` (audit, configs, monitor), debug.go, `GetTaskLogsWS`, `BatchDeleteTasks`, `ListTasks`, `BatchDeleteExecutions`, `BatchDeleteInjections`, `WS` and `notifications stream`.

---

## 8. Surprises / dead code

1. **Two endpoints permanently return 410 Gone** but still routed: `GetInjectionMetadata` (injections.go:130) and `TranslateFaultSpecs` (injections.go:677). Body literally `{"error":"endpoint removed; migrate to /inject with GuidedConfig"}`. Confirms the inject pipeline migration (legacy translate round-trip is dead) — no `guidedcli` package import in this slice; `producer.FriendlySpecToNode` (injections.go:866) is the in-process auto-detect that replaced it.

2. **Handlers reach into `repository`/`database` directly**, bypassing the service layer: `tasks.go:208` (WS pre-flight), all of `pedestal_helm.go`, and `health.go` (raw `SELECT 1`). The pedestalhelm CRUD is fully a handler-layer feature with no service abstraction — inconsistent with the rest of the codebase.

3. **`handlers/v2/pedestalhelm/`** is a separate package (`package pedestalhelm`) carved out of `handlers/v2` to dodge a build break. The package comment (verify.go:1-7) says explicitly "intentionally separated… so the pure pipeline can be unit-tested without dragging in the full server build graph (which currently has an unrelated compile break in injections.go)." That implies `injections.go` is currently broken in some build configuration.

4. **`SwaggerModelsDoc` (docs.go:38) has an empty function body** — pure swagger anchor for `consts.SSEEventName` registration. Not a real handler.

5. **`debug.go` handlers (`GetAllVars`, `GetVar`, `SetVar`) carry no `@Router` annotation** — they're mounted somewhere outside this slice (likely a debug-only route group).

6. **`GetMetrics` (system/monitor.go:31)** is `@Deprecated`, sets `Deprecation: true` and `Link: rel="successor-version"` headers, returns hardcoded data. Successor: `v2.GetSystemMetrics`.

7. **`AuditMiddleware` excludes `/system/monitor/namespace-locks`** but the actual route is `/system/monitor/namespaces/locks` (monitor.go:121). Stale string — namespace-lock POSTs would still be audited because the prefix never matches. (system/monitor.go:121 vs middleware/audit.go:135.)

8. **`UserBasedRateLimit` key generation is broken**: ratelimit.go:148 builds the key as `"user_" + string(rune(userID))` — converting an int to its Unicode rune. User IDs > 0x10FFFF or invalid rune ranges become the replacement char "\uFFFD", causing all of them to share one bucket. This is functionally wrong (should be `strconv.Itoa`).

9. **`anyPermission` swallows errors silently** (permission.go:177): `logrus.Warnf("Permission check error: %f", err)` (note `%f` for an error value) and `continue` — masks real DB failures and uses the wrong format verb.

10. **Two service token bypass paths in `permission.go`**: extractPermissionContext returns `(nil, "")` for service tokens (line 39-41), then `withPermissionCheck` re-checks `IsServiceToken(c)` before deciding to abort (lines 102-112). The first guard makes the second redundant; if the bool semantics ever drift, the bypass could become a hard auth failure for K8s job callers.

11. **Bonus surprise: `producer.ProduceAlgorithmExeuctionTasks`** (executions.go:251) — typo "Exeuction" instead of "Execution" in the public service symbol. Surfaces in handler call but is a service-side problem.

12. **`UploadDatapack` (injections.go:960)** has no `@x-api-type` annotation — the only `injections.go` POST that doesn't get into the SDK, even though the matching `UpdateGroundtruth` PUT (line 1015) is also missing. Means CLIs/SDKs can't trigger manual datapack upload through the generated client.
