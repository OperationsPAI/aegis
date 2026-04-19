# Topology slice: resource/asset modules

Scope: 8 modules under `src/module/` — container, dataset, evaluation, chaossystem, label, metric, pedestal, sdk.
Root: `/home/ddq/AoyangSpace/aegis/AegisLab/src`.

---

## 1. Per-module breakdown

### module/container

- Files: `api_types.go`, `build_gateway.go`, `build_gateway_test.go`, `core.go`, `file_store.go`, `file_store_test.go`, `handler.go`, `handler_service.go`, `module.go`, `repository.go`, `resolve.go`, `service.go`
- fx.Module wiring (`module.go:5-12`): Provides `NewRepository`, `NewBuildGateway`, `NewHelmFileStore`, `NewService`, `AsHandlerService`, `NewHandler`. No Invokes.
- HandlerService interface (`handler_service.go:12-28`):
  - `CreateContainer(ctx, *CreateContainerReq, userID int) (*ContainerResp, error)`
  - `DeleteContainer(ctx, id int) error`
  - `GetContainer(ctx, id int) (*ContainerDetailResp, error)`
  - `ListContainers(ctx, *ListContainerReq) (*dto.ListResp[ContainerResp], error)`
  - `UpdateContainer(ctx, *UpdateContainerReq, id int) (*ContainerResp, error)`
  - `ManageContainerLabels(ctx, *ManageContainerLabelReq, id int) (*ContainerResp, error)`
  - `CreateContainerVersion(ctx, *CreateContainerVersionReq, containerID, userID int) (*ContainerVersionResp, error)`
  - `DeleteContainerVersion(ctx, versionID int) error`
  - `GetContainerVersion(ctx, containerID, versionID int) (*ContainerVersionDetailResp, error)`
  - `ListContainerVersions(ctx, *ListContainerVersionReq, containerID int) (*dto.ListResp[ContainerVersionResp], error)`
  - `UpdateContainerVersion(ctx, *UpdateContainerVersionReq, containerID, versionID int) (*ContainerVersionResp, error)`
  - `SetContainerVersionImage(ctx, *SetContainerVersionImageReq, versionID int) (*SetContainerVersionImageResp, error)`
  - `SubmitContainerBuilding(ctx, *SubmitBuildContainerReq, groupID string, userID int) (*SubmitContainerBuildResp, error)`
  - `UploadHelmChart(ctx, *multipart.FileHeader, containerID, versionID, userID int) (*UploadHelmChartResp, error)`
  - `UploadHelmValueFile(ctx, *multipart.FileHeader, containerID, versionID, userID int) (*UploadHelmValueFileResp, error)`
- Handlers (all in `handler.go`):
  - `CreateContainer` — `POST /api/v2/containers` (`:41`)
  - `DeleteContainer` — `DELETE /api/v2/containers/{container_id}` (`:84`)
  - `GetContainer` — `GET /api/v2/containers/{container_id}` (`:114`)
  - `ListContainers` — `GET /api/v2/containers` (`:148`)
  - `UpdateContainer` — `PATCH /api/v2/containers/{container_id}` (`:187`)
  - `ManageContainerCustomLabels` — `PATCH /api/v2/containers/{container_id}/labels` (`:226`)
  - `CreateContainerVersion` — `POST /api/v2/containers/{container_id}/versions` (`:270`)
  - `DeleteContainerVersion` — `DELETE /api/v2/containers/{container_id}/versions/{version_id}` (`:319`)
  - `GetContainerVersion` — `GET /api/v2/containers/{container_id}/versions/{version_id}` (`:350`)
  - `ListContainerVersions` — `GET /api/v2/containers/{container_id}/versions` (`:387`)
  - `UpdateContainerVersion` — `PATCH /api/v2/containers/{container_id}/versions/{version_id}` (`:432`)
  - `SetContainerVersionImage` — `PATCH /api/v2/container-versions/{id}/image` (`:476`, `sdk:true`)
  - `SubmitContainerBuilding` — `POST /api/v2/containers/build` (`:520`)
  - `UploadHelmChart` — `POST /api/v2/containers/{container_id}/versions/{version_id}/helm-chart` (`:567`)
  - `UploadHelmValueFile` — `POST /api/v2/containers/{container_id}/versions/{version_id}/helm-values` (`:623`)
- Service methods (`service.go`): each CRUD method matches the interface. Notable:
  - `SubmitContainerBuilding` (`:392-436`) builds image-ref, prepares GitHub source via `build.PrepareGitHubSource`, then submits a `consts.TaskTypeBuildContainer` `dto.UnifiedTask` to Redis via `common.SubmitTaskWithDB`.
  - `UploadHelmChart` / `UploadHelmValueFile` call `helmFiles.SaveChart` / `helmFiles.SaveValueFile`, then persist via `repo.updateHelmConfig`.
  - Imports `module/label` for `label.NewRepository(tx).CreateOrUpdateLabelsFromItems` (`service.go:172`).
- Repository (`repository.go`, all operate on `*gorm.DB`, models in `aegis/model`):
  - Containers: `createContainer`, `updateContainer`, `deleteContainer` (soft, status=CommonDeleted), `getContainerByID`, `listContainers`, `checkContainerExistsWithDifferentType`.
  - UserContainer: `createUserContainer`, `removeUsersFromContainer`.
  - Labels: `addContainerLabels`, `clearContainerLabels`, `listContainerLabels`, `listLabelsByContainerID`, `listLabelIDsByKeyAndContainerID`, `batchDecreaseLabelUsages`.
  - ContainerVersion: `batchCreateContainerVersions`, `updateContainerVersion`, `deleteContainerVersion`, `getContainerVersionByID`, `listContainerVersions`, `listContainerVersionsByContainerID`, `batchGetContainerVersions`, `batchDeleteContainerVersions`, `updateContainerVersionImageColumns`.
  - ParameterConfig: `batchCreateOrFindParameterConfigs`, `listParameterConfigsByKeys`, `addContainerVersionEnvVars`, `listContainerVersionEnvVars`.
  - HelmConfig: `batchCreateHelmConfigs`, `addHelmConfigValues`, `listHelmConfigValues`, `getHelmConfigByContainerVersionID`, `updateHelmConfig`, and getter for `getRoleByName`.
- Stores / gateways / resolvers:
  - `HelmFileStore` (`file_store.go`) — writes `.tgz` chart + YAML value files under `<jfs.dataset_path>/helm-charts/` and `.../helm-values/`.
  - `BuildGateway` (`build_gateway.go`) — clones GitHub repo into `<jfs.container_path>/<imageName>/build_<ts>` via `exec.Command`; builds `registry/namespace/imageName:tag` refs. No direct BuildKit invocation here.
  - `core.go` — cross-module entry points: `Repository.CreateContainerCore`, `Repository.UploadHelmValueFileFromPath` (used by other modules with just a `*gorm.DB`).
  - `resolve.go` — `Repository.ResolveContainerVersions(refs, containerType, userID)` maps `dto.ContainerRef{Name,Version}` → `model.ContainerVersion`; `ListContainerVersionEnvVars`, `ListHelmConfigValues` (parameter spec + template rendering via `{{ .Field }}` regex `templateVarRegex` at `resolve.go:14`).
- Cross-module imports: `aegis/module/label` (service-layer), `aegis/service/common` (task submission), `aegis/infra/redis`, `aegis/consts`, `aegis/dto`, `aegis/model`, `aegis/httpx`, `aegis/middleware`, `aegis/utils`, `aegis/config`.

### module/dataset

- Files: `api_types.go`, `core.go`, `file_store.go`, `file_store_test.go`, `handler.go`, `handler_service.go`, `module.go`, `repository.go`, `resolve.go`, `service.go`
- fx.Module wiring (`module.go:5-11`): Provides `NewRepository`, `NewDatapackFileStore`, `NewService`, `AsHandlerService`, `NewHandler`.
- HandlerService interface (`handler_service.go:12-28`):
  - `CreateDataset`, `DeleteDataset`, `GetDataset`, `ListDatasets`, `SearchDatasets`, `UpdateDataset`, `ManageDatasetLabels`
  - `CreateDatasetVersion`, `DeleteDatasetVersion`, `GetDatasetVersion`, `ListDatasetVersions`, `UpdateDatasetVersion`
  - `GetDatasetVersionFilename(ctx, datasetID, versionID int) (string, error)`
  - `DownloadDatasetVersion(ctx, *zip.Writer, []utils.ExculdeRule, versionID int) error`
  - `ManageDatasetVersionInjections(ctx, *ManageDatasetVersionInjectionReq, versionID int) (*DatasetVersionDetailResp, error)`
- Handler routes (`handler.go`): `POST /api/v2/datasets` (`:42`), `DELETE /…/{dataset_id}` (`:85`), `GET /…/{dataset_id}` (`:115`), `GET /api/v2/datasets` (`:149`), `POST /api/v2/datasets/search` (`:186`), `PATCH /…/{dataset_id}` (`:225`), `PATCH /…/{dataset_id}/labels` (`:269`), versions: `POST /…/versions` (`:313`), `DELETE /…/versions/{version_id}` (`:362`), `GET /…/versions/{version_id}` (`:393`), `GET /…/versions` (`:430`), `PATCH /…/versions/{version_id}` (`:475`), `GET /…/versions/{version_id}/download` (`:521`, `portal+sdk`), `PATCH /api/v2/datasets/{dataset_id}/version/{version_id}/injections` (`:569`, `portal+sdk`).
- Service methods (`service.go`): CRUD + version CRUD match interface. Key cross-module behavior:
  - `DownloadDatasetVersion` (`:372`) → `repo.ListInjectionsByDatasetVersionID` → `datapacks.PackageToZip`.
  - `ManageDatasetLabels` → `label.NewRepository(tx).CreateOrUpdateLabelsFromItems` (`:192`).
  - `ManageDatasetVersionInjections` / `CreateDatasetVersion` link datapacks by resolving injection names via `repo.listInjectionIDsByNames`.
- Repository (`repository.go`) — operates on `model.Dataset`, `model.DatasetVersion`, `model.FaultInjection` (a.k.a. "datapack"), `model.DatasetVersionInjection`, `model.DatasetLabel`, `model.UserDataset`, `model.Label`. Notable: `searchDatasets` supports `dto.SearchReq[consts.DatasetField]`; `ListInjectionsByDatasetVersionID` joins `dataset_version_injections` and filters `state = DatapackBuildSuccess AND status != CommonDeleted` (`repository.go:383-398`).
- Stores / resolvers:
  - `DatapackFileStore` (`file_store.go:15-69`) — walks `<jfs.dataset_path>/<datapack.Name>/`, streams into a zip under path prefix `<DownloadFilename>/<datapackName>/...`. `IsAllowedPath` guards path traversal.
  - `core.go`: `Repository.CreateDatasetCore` (used when other modules need to seed datasets).
  - `resolve.go`: `Repository.ResolveDatasetVersions(refs, userID)` → `map[*dto.DatasetRef]model.DatasetVersion`.
- Cross-module imports: `aegis/module/label` (service), `aegis/consts`, `aegis/dto`, `aegis/model`, `aegis/utils`, `aegis/config`, `aegis/httpx`, `aegis/middleware`.

### module/evaluation

- Files: `api_types.go`, `execution_query.go`, `handler.go`, `handler_service.go`, `module.go`, `repository.go`, `service.go`, `service_test.go`
- fx.Module wiring (`module.go:5-11`): Provides `NewRepository`, `newExecutionQuerySource`, `NewService`, `AsHandlerService`, `NewHandler`. `RemoteQueryOption()` (`execution_query.go:74-76`) is `fx.Decorate(newRemoteExecutionQuerySource)` used in "dedicated resource-service" mode to force orchestrator-RPC query source.
- HandlerService interface (`handler_service.go:10-16`):
  - `ListDatapackEvaluationResults(ctx, *BatchEvaluateDatapackReq, userID int) (*BatchEvaluateDatapackResp, error)`
  - `ListDatasetEvaluationResults(ctx, *BatchEvaluateDatasetReq, userID int) (*BatchEvaluateDatasetResp, error)`
  - `ListEvaluations(ctx, *ListEvaluationReq) (*dto.ListResp[EvaluationResp], error)`
  - `GetEvaluation(ctx, id int) (*EvaluationResp, error)`
  - `DeleteEvaluation(ctx, id int) error`
- Handler routes: `POST /api/v2/evaluations/datapacks` (`:37`), `POST /api/v2/evaluations/datasets` (`:80`), `GET /api/v2/evaluations` (`:122`), `GET /api/v2/evaluations/{id}` (`:157`), `DELETE /api/v2/evaluations/{id}` (`:186`).
- Service methods (`service.go`):
  - `ListDatapackEvaluationResults` → `container.NewRepository(db).ResolveContainerVersions`, then per-spec calls `query.ListEvaluationExecutionsByDatapack`. Persists matches via `persistEvaluations` into `model.Evaluation` (best-effort, errors logged only).
  - `ListDatasetEvaluationResults` → uses both `container.NewRepository.ResolveContainerVersions` and `dataset.NewRepository.ResolveDatasetVersions`; queries `query.ListEvaluationExecutionsByDataset`; groups executions by datapack name; computes `NotExecutedDatapacks`.
  - `ListEvaluations`, `GetEvaluation`, `DeleteEvaluation` — plain repo proxies.
- Repository (`repository.go`): `ListEvaluations` (selects subset of columns, order by `updated_at DESC`), `GetEvaluationByID`, `DeleteEvaluation` (soft, `status=CommonDeleted`). Target entity: `model.Evaluation`.
- Stores / gateways: `executionQuerySource` (`execution_query.go`) — adapter that prefers `orchestratorclient.Client` if available/enabled, otherwise falls back to local `execution.Service`. `requireRemote` flag (used by `RemoteQueryOption`) blocks local fallback.
- Cross-module imports: `aegis/module/container`, `aegis/module/dataset`, `aegis/module/execution`, `aegis/internalclient/orchestratorclient`, `aegis/consts`, `aegis/dto`, `aegis/model`.

### module/chaossystem

- Files: `api_types.go`, `handler.go`, `handler_service.go`, `module.go`, `repository.go`, `service.go`
- fx.Module wiring (`module.go:5-10`): Provides `NewRepository`, `NewService`, `AsHandlerService`, `NewHandler`.
- HandlerService interface (`handler_service.go:10-18`):
  - `ListSystems(ctx, *ListChaosSystemReq) (*dto.ListResp[ChaosSystemResp], error)`
  - `GetSystem(ctx, id int) (*ChaosSystemResp, error)`
  - `CreateSystem(ctx, *CreateChaosSystemReq) (*ChaosSystemResp, error)`
  - `UpdateSystem(ctx, id int, *UpdateChaosSystemReq) (*ChaosSystemResp, error)`
  - `DeleteSystem(ctx, id int) error`
  - `UpsertMetadata(ctx, id int, *BulkUpsertSystemMetadataReq) error`
  - `ListMetadata(ctx, id int, metadataType string) ([]SystemMetadataResp, error)`
- Handler routes: `GET /api/v2/systems` (`:35`), `GET /api/v2/systems/{id}` (`:67`), `POST /api/v2/systems` (`:96`), `PUT /api/v2/systems/{id}` (`:126`), `DELETE /api/v2/systems/{id}` (`:158`), `POST /api/v2/systems/{id}/metadata` (`:186`), `GET /api/v2/systems/{id}/metadata` (`:218`).
- Service methods (`service.go`):
  - `CreateSystem` (`:50-81`) / `UpdateSystem` (`:83-131`) validate `NsPattern`/`ExtractPattern` regexes, persist, then call `chaos.RegisterSystem(chaos.SystemConfig{...})` from `github.com/OperationsPAI/chaos-experiment/handler`. Errors from `RegisterSystem` are logged, not returned.
  - `DeleteSystem` (`:133-148`) blocks deletion of `IsBuiltin` systems (`consts.ErrBadRequest`) and calls `chaos.UnregisterSystem`.
  - `UpsertMetadata` / `ListMetadata` manage `model.SystemMetadata` by `SystemName`.
- Repository (`repository.go`): `ListSystems`, `GetSystemByID`, `CreateSystem`, `UpdateSystem`, `DeleteSystem`, `UpsertSystemMetadata`, `ListSystemMetadata`. Target entities: `model.System`, `model.SystemMetadata`.
- Cross-module imports: `github.com/OperationsPAI/chaos-experiment/handler` (external), `aegis/consts`, `aegis/dto`, `aegis/model`. No internal `module/*` or `infra/*` imports beyond this.
- Bootstrap: `service/initialization/systems.go:26-78` seeds 6 built-in systems (`train-ticket`, `sock-shop`, `social-network`, `online-boutique`, `hotel-reservation`, `media-microsvc`) with `NsPattern=^ts\d+$` etc., then calls `chaos.RegisterSystem` for each enabled row and finally `chaos.SetMetadataStore(common.NewDBMetadataStore(db))`. Also `config.GetChaosSystemConfigManager().Reload(...)`.

### module/label

- Files: `api_types.go`, `core.go`, `handler.go`, `handler_service.go`, `module.go`, `repository.go`, `service.go`
- fx.Module wiring (`module.go:5-10`): Provides `NewRepository`, `NewService`, `AsHandlerService`, `NewHandler`.
- HandlerService interface (`handler_service.go:10-17`):
  - `BatchDelete(ctx, []int) error`
  - `Create(ctx, *CreateLabelReq) (*LabelResp, error)`
  - `Delete(ctx, id int) error`
  - `GetDetail(ctx, id int) (*LabelDetailResp, error)`
  - `List(ctx, *ListLabelReq) (*dto.ListResp[LabelResp], error)`
  - `Update(ctx, *UpdateLabelReq, id int) (*LabelResp, error)`
- Handler routes: `POST /api/v2/labels/batch-delete` (`:35`), `POST /api/v2/labels` (`:69`), `DELETE /api/v2/labels/{label_id}` (`:103`), `GET /api/v2/labels/{label_id}` (`:131`), `GET /api/v2/labels` (`:165`), `PATCH /api/v2/labels/{label_id}` (`:201`).
- Service methods (`service.go`):
  - `BatchDelete` (`:23-86`) — in a transaction, batches association removals across 5 entity kinds (container/dataset/project/injection/execution), aggregates per-label decrement counts, then `BatchUpdateLabels` and `BatchDeleteLabels`.
  - `Delete` (`:110-155`) — per-label analogue.
  - `Create` / `Update` / `List` / `GetDetail` — standard CRUD; uses `createLabelCore` → `repo.CreateLabelCore`.
  - Internal helper `removeAssociationsFromLabels` + 5 wrappers (`removeContainersFromLabels`, `removeDatasetsFromLabels`, `removeProjectsFromLabels`, `removeInjectionsFromLabels`, `removeExecutionsFromLabels`).
- Repository (`repository.go`): 30+ methods operating on `model.Label`, plus join-table helpers for each category (container_labels, dataset_labels, project_labels, injection_labels, execution_labels). Key public: `ListLabelsByID`, `BatchUpdateLabels`, `BatchDeleteLabels`, `GetLabelByKeyAndValue`, `CreateLabel`, `UpdateLabel`, `GetLabelByID`, `DeleteLabel`, `ListLabels`, `BatchDecreaseLabelUsages`, plus `Remove*FromLabel(s)` and `List*LabelCounts`.
- Core (`core.go`): `Repository.CreateLabelCore` (upsert-on-key-value) and `Repository.CreateOrUpdateLabelsFromItems(db, items, category)` which `container`, `dataset`, `project`, `injection`, `execution` modules all call to idempotently attach labels + bump usage.
- Cross-module imports: only `aegis/consts`, `aegis/dto`, `aegis/model`, `aegis/utils`. The label module is a *leaf* — everyone calls into it, it calls nothing in `module/*`.

### module/metric

- Files: `api_types.go`, `handler.go`, `handler_service.go`, `module.go`, `repository.go`, `service.go`
- fx.Module wiring (`module.go:5-10`): Provides `NewRepository`, `NewService`, `AsHandlerService`, `NewHandler`.
- HandlerService interface (`handler_service.go:6-10`):
  - `GetInjectionMetrics(ctx, *GetMetricsReq) (*InjectionMetrics, error)`
  - `GetExecutionMetrics(ctx, *GetMetricsReq) (*ExecutionMetrics, error)`
  - `GetAlgorithmMetrics(ctx, *GetMetricsReq) (*AlgorithmMetrics, error)`
- Handler routes: `GET /api/v2/metrics/injections` (`:35`), `GET /api/v2/metrics/executions` (`:67`), `GET /api/v2/metrics/algorithms` (`:99`).
- Service methods (`service.go`):
  - `GetInjectionMetrics` — queries `model.FaultInjection` with filter `created_at` / `fault_type`; returns `TotalCount`, `StateDistrib[string]int`, `FaultTypeDistrib[string]int`, success/failed count (state `2`/`3`), min/max/avg duration, success rate.
  - `GetExecutionMetrics` — same shape over `model.Execution`, uses `exec.Duration`; filter by `algorithm_id`.
  - `GetAlgorithmMetrics` — lists `model.Container` where `type = 2` (algorithm, via repository `ListAlgorithmContainers` — magic number — `repository.go:33-39`), then per-algorithm iterates executions.
- Repository (`repository.go`, 40 lines): `ListFaultInjections(func(*gorm.DB) *gorm.DB)`, `ListExecutions(...)`, `ListAlgorithmContainers()`. Targets: `model.FaultInjection`, `model.Execution`, `model.Container`.
- Cross-module imports: only `aegis/model`.

### module/pedestal

- Files: `api_types.go`, `handler.go`, `module.go`, `repository.go`, `verify.go`
- fx.Module wiring (`module.go:5-8`): Provides `NewRepository`, `NewHandler`. No Service/HandlerService layers — the handler holds `repo` + a `Runner` field directly.
- HandlerService interface: NONE (unique pattern in this slice). Handler uses repo+runner directly.
- Handler routes: `GET /api/v2/pedestal/helm/{container_version_id}` (`:38`, `sdk:true`), `PUT /api/v2/pedestal/helm/{container_version_id}` (`:75`), `POST /api/v2/pedestal/helm/{container_version_id}/verify` (`:119`).
- Repository (`repository.go`): `GetHelmConfigByContainerVersionID`, `UpsertHelmConfig`. Target: `model.HelmConfig`.
- Stores / gateways / verifier (`verify.go`):
  - `Config`, `Check`, `Result` value types.
  - `Runner` interface + `RealRunner` struct that shells out to `helm repo add --force-update`, `helm repo update`, `helm pull` via `exec.Command`.
  - `Run(runner, cfg, valueFileVerifier)` drives a 4-step pipeline: repo_add → repo_update → helm_pull (tmpdir) → value_file (YAML parse, asserts `image.repository` is string and `image.tag` is scalar).
- Cross-module imports: only `aegis/dto`, `aegis/middleware`, `aegis/model` (no other `module/*`). Standalone.

### module/sdk

- Files: `api_types.go`, `handler.go`, `models.go`, `module.go`, `repository.go`, `service.go`, `service_test.go`
- fx.Module wiring (`module.go:5-9`): Provides `NewRepository`, `NewService`, `NewHandler`. No `AsHandlerService`; handler is wired directly via `*Service`.
- HandlerService interface: NONE. Handler depends on concrete `*Service`.
- Handler routes: `GET /api/v2/sdk/evaluations` (`:36`), `GET /api/v2/sdk/evaluations/{id}` (`:68`), `GET /api/v2/sdk/evaluations/experiments` (`:92`), `GET /api/v2/sdk/datasets` (`:116`). All tagged `sdk:true`.
- Service methods (`service.go`): `ListEvaluations`, `GetEvaluation`, `ListExperiments`, `ListDatasetSamples` — all pass through to repo.
- Repository (`repository.go`): reads from 2 external tables:
  - `data` (via `SDKDatasetSample.TableName()` = `"data"`, `models.go:22`) — Python SDK's dataset sample table.
  - `evaluation_data` (via `SDKEvaluationSample.TableName()` = `"evaluation_data"`, `models.go:55`) — Python SDK's evaluation result table.
  - `isTableNotExistError` (`repository.go:100-108`) swallows "doesn't exist" / "no such table" errors and returns empty results — tolerant read-only bridge.
- Models (`models.go`):
  - `SDKDatasetSample` — 12 columns including `Dataset`, `Index`, `Source`, `Question`, `Answer`, `Meta` (JSON), `Tags` (JSON).
  - `SDKEvaluationSample` — 25 columns including `ExpID`, `Stage`, `TraceID`, `TraceURL`, `Response`, `ExtractedFinalAnswer`, `Correct`, `Confidence`, `AgentType`, `ModelName`.
- Cross-module imports: only `aegis/dto`. Does not touch AegisLab's own `datasets` / `evaluations` tables at all.

---

## 2. Dataset & datapack storage layout

Filesystem base paths are pulled from viper config keys (`config.GetString(...)`):

| Artifact | Config key | Path template | Code |
|---|---|---|---|
| Helm chart package (.tgz) | `jfs.dataset_path` | `<base>/helm-charts/<containerName>_chart_<unixTs><ext>` | `module/container/file_store.go:29-40` |
| Helm values file (.yaml) | `jfs.dataset_path` | `<base>/helm-values/<containerName>_values_<unixTs><ext>` | `module/container/file_store.go:61-82` |
| Container build source (git clone) | `jfs.container_path` | `<base>/<imageName>/build_<unixTs>/` (and `build_final_<unixTs>/` when `SubPath` is copied out) | `module/container/build_gateway.go:45, 76` |
| Container image ref | `harbor.registry` + `harbor.namespace` | `<registry>/<namespace>/<imageName>:<tag>` | `module/container/build_gateway.go:41` |
| Datapack zip (download) | `jfs.dataset_path` | walks `<base>/<datapack.Name>/…` into a streaming zip under prefix `<consts.DownloadFilename>/<datapackName>/` | `module/dataset/file_store.go:37-63` |

Note: both `HelmFileStore` (container module) and `DatapackFileStore` (dataset module) share the same `jfs.dataset_path` base — i.e. Helm charts/values live under the dataset JuiceFS mount, not the container mount.

## 3. Datapack query

Out of scope per task instructions (skipped).

## 4. Container build pipeline

- The container module does NOT invoke BuildKit directly. `module/container/build_gateway.go` only: (a) shells out to `git clone` / `git checkout` / `git -C … checkout` via `g.commandRunner` (default `exec.Command`), (b) computes the `registry/namespace/image:tag` string via `BuildImageRef`. No Docker/BuildKit references in this file.
- `SubmitContainerBuilding` (`service.go:392-436`):
  1. `build.PrepareGitHubSource(req)` — clones `https://github.com/<GithubRepository>.git` (with optional `GithubToken` and `GithubBranch`) into `<jfs.container_path>/<imageName>/build_<ts>/`, optionally checks out `GithubCommit`, optionally copies `SubPath` into a fresh `build_final_<ts>` dir and deletes the original clone (`build_gateway.go:44-87`).
  2. Validates the request (`req.ValidateInfoContent(sourcePath)`, `req.Options.ValidateRequiredFiles(sourcePath)`).
  3. Packages a `dto.UnifiedTask{Type: consts.TaskTypeBuildContainer, Immediate: true, Payload: { BuildImageRef, BuildSourcePath, BuildBuildOptions }}` and calls `common.SubmitTaskWithDB(ctx, db, redis, task)`.
- Actual BuildKit invocation lives in `service/consumer/build_container.go`:
  - `dispatchTask` routes `TaskTypeBuildContainer` → `executeBuildContainer` (`service/consumer/distribute_tasks.go:35`).
  - `executeBuildContainer` → `buildImageAndPush(ctx, buildKitGateway, payload, logEntry)` uses `aegis/infra/buildkit.Gateway` and the `github.com/moby/buildkit/client` SDK (`build_container.go:20-28, 51-62, 187-289`), driving `buildkitclient.SolveOpt` with exporter type `ExporterImage`, session auth provider, and progress UI.
- So the module/container code is the task *producer* (HTTP → task queue). The consumer subsystem (different slice) is what actually runs BuildKit.
- `build_gateway_test.go` covers `BuildImageRef` string formatting (`:13-22`) and `PrepareGitHubSource` sub-path copying using a real local git repo + `viper.Set("jfs.container_path", tmpDir)`.

## 5. SDK module

- `module/sdk/` exposes 4 read-only `GET` endpoints under `/api/v2/sdk/…` (enumerated above) that bridge from AegisLab's Gin layer to two external tables owned by the Python SDK: `data` (`SDKDatasetSample`) and `evaluation_data` (`SDKEvaluationSample`).
- Models carry `func (X) TableName() string { return "data" | "evaluation_data" }` — explicit table overrides, and comments state "Do NOT add this to AutoMigrate - the SDK creates and manages this table." (`models.go:6-7, 25-26`).
- Repository is resilient to missing tables: `isTableNotExistError` (`repository.go:100-108`) matches any of "doesn't exist", "does not exist", "no such table" and returns empty results rather than an error. Same behavior across MySQL and SQLite.
- No writes, no AutoMigrate registration, no cross-module dependencies other than `dto`/`gorm`.

## 6. Chaos system

- Yes, it still uses `chaos.RegisterSystem` from `github.com/OperationsPAI/chaos-experiment/handler` — called in three places:
  - `module/chaossystem/service.go:72` — after `CreateSystem` writes the row.
  - `module/chaossystem/service.go:122` — after `UpdateSystem` writes the row.
  - `service/initialization/systems.go:51` — on startup for every enabled system row.
- Bootstrap happens in `service/initialization/systems.go:InitializeSystems(db)`:
  1. `config.SetChaosConfigDB(db)` — gives the `ChaosSystemConfig` singleton access to the `System` table.
  2. Seeds 6 built-in `model.System` rows if missing (names: `train-ticket`, `sock-shop`, `social-network`, `online-boutique`, `hotel-reservation`, `media-microsvc`; each with `NsPattern`, `ExtractPattern`, `Count=1`, `IsBuiltin=true`).
  3. Loads all enabled systems via `newBootstrapStore(db).listEnabledSystems()` and calls `chaos.RegisterSystem(chaos.SystemConfig{Name, NsPattern, DisplayName})` for each.
  4. `chaos.SetMetadataStore(common.NewDBMetadataStore(db))` — installs the global metadata-store bridge so chaos-experiment can read per-system metadata.
  5. `config.GetChaosSystemConfigManager().Reload(...)` — re-loads the singleton that might have been initialized earlier with `chaosConfigDB=nil`.
- `DeleteSystem` blocks deletion of `IsBuiltin` rows and calls `chaos.UnregisterSystem`. Registration errors are warned-and-continued, not returned, so a failure to register with chaos-experiment won't abort the API call.

## 7. Metric module

- Three metric surfaces, all driven by a date-range + optional filter:
  - `InjectionMetrics` sources `model.FaultInjection` filtered by `created_at` range and optional `fault_type`; computes `TotalCount`, state/fault-type distributions, success (`State==2`) and failed (`State==3`) counts, plus duration stats (`EndTime.Sub(StartTime)`).
  - `ExecutionMetrics` sources `model.Execution` filtered by `created_at` + optional `algorithm_id`; similar shape, uses `exec.Duration` field directly.
  - `AlgorithmMetrics` lists `model.Container` where `type = 2` (hardcoded int — algorithm container type), then per algorithm loops executions to produce `AlgorithmMetricItem{ID, Name, ExecutionCount, SuccessCount, FailedCount, SuccessRate, AvgDuration}`.
- States `2` and `3` are magic numbers (likely `StateSuccess` / `StateFailed`) — literals throughout `service.go:163-167, 202-207, 237-242`.
- Repository has zero filtering logic of its own; all predicates are passed in as `func(*gorm.DB) *gorm.DB` closures (`repository.go:17-31`).
- No cross-module imports beyond `aegis/model`; pure aggregator over GORM.

## 8. Cross-module edges

- **evaluation** is the biggest consumer: imports `module/container` (for `ResolveContainerVersions`), `module/dataset` (for `ResolveDatasetVersions`), and `module/execution` (for `EvaluationExecutionsByDatapackReq`/`ByDatasetReq` and the local `execution.Service`).
- **container → label**, **dataset → label**: both import `aegis/module/label` at the service layer for `label.NewRepository(tx).CreateOrUpdateLabelsFromItems` (`container/service.go:13, 172`; `dataset/service.go:12, 192`). Label is a leaf with no outbound `module/*` imports.
- **container → service/common, infra/redis**: `container.Service` holds a `*redis.Gateway` and calls `common.SubmitTaskWithDB` to enqueue BuildContainer tasks.
- **chaossystem** has zero internal `module/*` imports — it depends only on external `github.com/OperationsPAI/chaos-experiment/handler` and `aegis/model`. Bootstrap is done by `service/initialization/systems.go`, which is outside the module.
- **metric**, **pedestal**, **sdk** have no `module/*` fan-out (pedestal imports `aegis/middleware` only). They are stand-alone reporting/bridging surfaces.
- **label**: fan-in from container, dataset, project, injection, execution — its service-layer `BatchDelete` knows about all 5 join tables (via its own repo), but it does not `import` those modules; the coupling is in the schema/table names, not Go imports.

## 9. Surprises

- **`pedestal.Handler` holds a `Runner` field** (`handler.go:17-23`) that is never overridable via fx — `NewHandler` always sets `RealRunner{}`. Tests would have to construct the Handler manually to stub helm.
- **`sdk` and `pedestal` modules skip the `AsHandlerService` interface pattern** that every other module in this slice uses. Handlers depend on concrete `*Service` (sdk) or `*Repository` (pedestal). Inconsistent with the module convention stated in the task.
- **`container.Service.createContainerCore` in `core.go:7-10`** calls `NewService(r, NewBuildGateway(), NewHelmFileStore(), nil)` — passing `nil` for `redis *redis.Gateway`. Safe only because `createContainerCore` never touches `redis`, but this is a time bomb if that invariant changes.
- **`metric` hardcodes `type = 2`** for algorithm containers (`repository.go:35`) and `state = 2`/`state = 3` for success/failed (`service.go:163-167`). Should reference `consts.ContainerTypeAlgorithm` and `consts.Execution*` enums.
- **`dataset.ManageDatasetVersionInjections`** does `version.FileCount = version.FileCount + len(req.AddDatapacks) - len(req.RemoveDatapacks)` (`service.go:440`) BEFORE it has verified that the linked injections actually succeeded — stale `FileCount` if the `linkDatapacksToDatasetVersion` rollback-and-retry semantics ever diverge from strict add-all-or-nothing (currently it does error out cleanly inside a `tx.Transaction`, so it's okay, but it's brittle).
- **`container.BuildGateway.PrepareGitHubSource`** embeds `GithubToken` directly in the repo URL (`build_gateway.go:32`) — it will appear in any `git` subprocess argv / `ps` output on the consumer host.
- **`evaluation.persistEvaluations`** swallows JSON marshal failures with `eval.ResultJSON = "{}"` and logs only a warning; DB write errors are also warn-only (`service.go:286, 294`). Evaluation persistence is "best effort" — the HTTP response succeeds even when `model.Evaluation` rows weren't saved.
- **`chaossystem.Service.CreateSystem / UpdateSystem`** log-and-continue on `chaos.RegisterSystem` failure (`service.go:76-78, 126-128`). The API returns success even when chaos-experiment rejects the config — the system row exists in MySQL but faults against it will fail because the handler isn't registered.
- **`label` repository mixes two patterns**: public methods take an explicit `db *gorm.DB` parameter (e.g., `ListLabelsByID(db, ids)`) while others don't (`listLabelsByConditions`). Callers must know which style to use, and `useDB(db *gorm.DB)` at `repository.go:275` quietly falls back to the struct's `db` when passed nil.
- **`module/container/resolve.go:14` regex `templateVarRegex`** rewrites `{{ .Field }}` with reflect-based lookups. `renderTemplate` uses `strings.ReplaceAll` for both `{{.Field}}` and `{{ .Field }}` spacings but not tabs or other whitespace variants, so `{{\t.Field\t}}` silently stays unrendered.
- **`dataset.DatapackFileStore.packageDatapackToZip`** silently drops any file where `utils.MatchFile(fileName, rule)` returns true (no return audit) — the client gets a smaller zip with no indication of what was filtered.
