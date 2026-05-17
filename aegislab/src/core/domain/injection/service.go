package injection

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/k8s"
	loki "aegis/platform/loki"
	redis "aegis/platform/redis"
	"aegis/platform/model"
	container "aegis/core/domain/container"
	dataset "aegis/core/domain/dataset"
	label "aegis/crud/iam/label"
	"aegis/core/orchestrator/common"
	"aegis/platform/utils"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// allocatedSlot pairs the namespace chosen by AllocateNamespaceForRestart
// with the pre-generated traceID under which the namespace lock was taken.
// Both flow into the per-batch task built later: namespace into the
// payload's RestartRequiredNamespace, traceID into task.TraceID. Fresh
// indicates a bootstrapped slot (no workload yet); the submit path must
// skip BuildInjection pod-listing and trust RestartPedestal to install
// before the inject task runs.
type allocatedSlot struct {
	namespace string
	traceID   string
	fresh     bool
}

// batchNeedsAutoAllocate reports whether at least one config in the batch
// has an empty namespace and would therefore benefit from server-side
// allocation. Returns false when every config already names a namespace
// (caller leaves the explicit-ns path of #164 alone).
func batchNeedsAutoAllocate(batch []guidedcli.GuidedConfig) bool {
	for _, cfg := range batch {
		if strings.TrimSpace(cfg.Namespace) == "" {
			return true
		}
	}
	return false
}

// defaultWorkloadProbe is the production WorkloadProbe used by the
// AutoAllocate submit path. Returns true when the namespace contains at
// least one pod (any phase). Tests override this seam.
func defaultWorkloadProbe() WorkloadProbe {
	gw := k8s.NewGateway(nil)
	return func(ctx context.Context, namespace string) (bool, error) {
		return gw.NamespaceHasWorkload(ctx, namespace)
	}
}

// ChaosSystemWriter is the narrow contract injection.Service needs from
// chaossystem to register guided namespaces with the system's namespace
// count. Declared here (rather than imported from chaossystem) to avoid
// the chaossystem→initialization→consumer→execution→injection cycle —
// `*chaossystem.Service` satisfies this interface structurally and is
// wired in at fx provide-time via a thin app-level adapter.
type ChaosSystemWriter interface {
	EnsureCountForNamespace(ctx context.Context, systemName, namespace string) (bool, error)
}

// TaskCanceller is the narrow contract injection.Service needs to cancel
// the task backing an injection. Implemented by *task.Service via an
// app-level adapter so this package owns its own DTO shape and avoids
// pulling task into the injection import graph.
type TaskCanceller interface {
	CancelTask(ctx context.Context, taskID string) (*CancelInjectionTaskResult, error)
}

// CancelInjectionTaskResult is the structural projection of the underlying
// task cancel response consumed by injection.Service.
type CancelInjectionTaskResult struct {
	TaskID            string
	State             string
	Message           string
	DeletedPodChaos   []string
	RemovedRedisTasks []string
}

type Service struct {
	repo          *Repository
	store         DatapackStorage
	lokiClient    *loki.Client
	containers    container.Reader
	datasets      dataset.Reader
	labels        label.Writer
	redis         *redis.Gateway
	chaosSystems  ChaosSystemWriter
	taskCanceller TaskCanceller
}

func NewService(repo *Repository, store DatapackStorage, lokiClient *loki.Client, containers container.Reader, datasets dataset.Reader, labels label.Writer, redis *redis.Gateway, chaosSystems ChaosSystemWriter, taskCanceller TaskCanceller) *Service {
	return &Service{
		repo:          repo,
		store:         store,
		lokiClient:    lokiClient,
		containers:    containers,
		datasets:      datasets,
		labels:        labels,
		redis:         redis,
		chaosSystems:  chaosSystems,
		taskCanceller: taskCanceller,
	}
}

func (s *Service) ListProjectInjections(ctx context.Context, req *ListInjectionReq, projectID int) (*dto.ListResp[InjectionResp], error) {
	var project model.Project
	if err := s.repo.db.Where("id = ?", projectID).First(&project).Error; err != nil {
		if errors.Is(err, consts.ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: project id %d not found", consts.ErrNotFound, projectID)
		}
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	limit, offset := req.ToGormParams()
	injections, total, err := s.repo.listProjectInjectionsView(projectID, limit, offset, req.ToFilterOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to list injections for project %d: %w", projectID, err)
	}

	items := make([]InjectionResp, 0, len(injections))
	for _, injection := range injections {
		items = append(items, *NewInjectionResp(&injection))
	}

	return &dto.ListResp[InjectionResp]{
		Items:      items,
		Pagination: req.ConvertToPaginationInfo(total),
	}, nil
}

func (s *Service) Search(ctx context.Context, req *SearchInjectionReq, projectID *int) (*dto.SearchResp[InjectionDetailResp], error) {
	if req == nil {
		return nil, fmt.Errorf("search injection request is nil")
	}
	injections, total, err := s.repo.searchInjections(req, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to search injections: %w", err)
	}
	items := make([]InjectionDetailResp, 0, len(injections))
	for _, injection := range injections {
		items = append(items, *NewInjectionDetailResp(&injection))
	}

	resp := &dto.SearchResp[InjectionDetailResp]{
		Pagination: req.ConvertToPaginationInfo(total),
	}
	if len(req.GroupBy) > 0 {
		resp.Groups = dto.BuildGroupTree(items, req.GroupBy)
	} else {
		resp.Items = items
	}
	return resp, nil
}

func (s *Service) ListNoIssues(ctx context.Context, req *ListInjectionNoIssuesReq, projectID *int) ([]InjectionNoIssuesResp, error) {
	if len(req.Labels) == 0 {
		return nil, nil
	}

	labelConditions := make([]map[string]string, 0, len(req.Labels))
	for _, item := range req.Labels {
		parts := splitLabelCondition(item)
		labelConditions = append(labelConditions, map[string]string{"key": parts[0], "value": parts[1]})
	}

	opts, err := req.Convert()
	if err != nil {
		return nil, fmt.Errorf("invalid time range: %w", err)
	}

	records, err := s.repo.listIssuesFreeInjections(labelConditions, &opts.CustomStartTime, &opts.CustomEndTime, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to list fault injections without issues: %w", err)
	}

	items := make([]InjectionNoIssuesResp, 0, len(records))
	for i, record := range records {
		resp, err := NewInjectionNoIssuesResp(record)
		if err != nil {
			return nil, fmt.Errorf("failed to create InjectionNoIssuesResp at index %d: %w", i, err)
		}
		items = append(items, *resp)
	}
	return items, nil
}

func (s *Service) ListWithIssues(ctx context.Context, req *ListInjectionWithIssuesReq, projectID *int) ([]InjectionWithIssuesResp, error) {
	if len(req.Labels) == 0 {
		return nil, nil
	}

	labelConditions := make([]map[string]string, 0, len(req.Labels))
	for _, item := range req.Labels {
		parts := splitLabelCondition(item)
		labelConditions = append(labelConditions, map[string]string{"key": parts[0], "value": parts[1]})
	}

	opts, err := req.Convert()
	if err != nil {
		return nil, fmt.Errorf("invalid time range: %w", err)
	}

	records, err := s.repo.listIssueInjections(labelConditions, &opts.CustomStartTime, &opts.CustomEndTime, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to list fault injections without issues: %w", err)
	}

	items := make([]InjectionWithIssuesResp, 0, len(records))
	for _, record := range records {
		resp, err := NewInjectionWithIssuesResp(record)
		if err != nil {
			return nil, fmt.Errorf("failed to create InjectionNoIssuesResp: %w", err)
		}
		items = append(items, *resp)
	}
	return items, nil
}

func (s *Service) SubmitFaultInjection(ctx context.Context, req *SubmitInjectionReq, groupID string, userID int, projectID *int) (*SubmitInjectionResp, error) {
	if req == nil {
		return nil, fmt.Errorf("submit injection request is nil")
	}
	db := s.repo.db

	if projectID == nil {
		project, err := s.repo.resolveProject(req.ProjectName)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("%w: project %s not found", consts.ErrNotFound, req.ProjectName)
			}
			return nil, fmt.Errorf("failed to get project: %w", err)
		}
		projectID = &project.ID
	}

	pedestalVersionResults, err := s.containers.ResolveContainerVersions([]*dto.ContainerRef{&req.Pedestal.ContainerRef}, consts.ContainerTypePedestal, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to map pedestal container ref to version: %w", err)
	}
	pedestalVersion, exists := pedestalVersionResults[&req.Pedestal.ContainerRef]
	if !exists {
		return nil, fmt.Errorf("pedestal version not found for container: %s (version: %s)", req.Pedestal.Name, req.Pedestal.Version)
	}

	helmConfig, err := s.repo.loadPedestalHelmConfig(pedestalVersion.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: helm config not found for pedestal version id %d", consts.ErrNotFound, pedestalVersion.ID)
		}
		return nil, fmt.Errorf("failed to get helm config: %w", err)
	}

	params := flattenYAMLToParameters(req.Pedestal.Payload, "")
	helmValues, err := s.containers.ListHelmConfigValues(params, helmConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to render pedestal helm values: %w", err)
	}

	helmConfigItem := dto.NewHelmConfigItem(helmConfig)
	helmConfigItem.DynamicValues = helmValues

	pedestalItem := dto.NewContainerVersionItem(&pedestalVersion)
	pedestalItem.Extra = helmConfigItem

	benchmarkVersionResults, err := s.containers.ResolveContainerVersions([]*dto.ContainerRef{&req.Benchmark.ContainerRef}, consts.ContainerTypeBenchmark, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to map benchmark container ref to version: %w", err)
	}
	benchmarkVersion, exists := benchmarkVersionResults[&req.Benchmark.ContainerRef]
	if !exists {
		return nil, fmt.Errorf("benchmark version not found for container: %s (version: %s)", req.Benchmark.Name, req.Benchmark.Version)
	}

	benchmarkVersionItem := dto.NewContainerVersionItem(&benchmarkVersion)
	envVars, err := s.containers.ListContainerVersionEnvVars(req.Benchmark.EnvVars, &benchmarkVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to list benchmark env vars: %w", err)
	}
	benchmarkVersionItem.EnvVars = envVars

	guidedSpecs := req.GuidedSpecs()
	if len(guidedSpecs) == 0 {
		return nil, fmt.Errorf("no guided specs available for fault injection")
	}

	// #166: AutoAllocate path — for any batch whose configs leave namespace
	// empty, pick a free deployed slot from the system's pool *now*, before
	// BuildInjection's submit-time pod listing runs. The chosen ns is
	// locked under a pre-generated traceID; the matching task built below
	// inherits that traceID so RestartPedestal's same-owner re-acquire
	// finds the lock already held by itself. If allocation fails for any
	// batch, this short-circuits and the request returns the error — no
	// rollback of earlier successful allocations in v1; their locks expire
	// naturally because the corresponding tasks were never submitted.
	allocations := make(map[int]allocatedSlot, len(guidedSpecs))
	if req.AutoAllocate {
		// Sized to comfortably cover restart + injection + a slack buffer.
		lockEndTime := time.Now().Add(time.Duration(consts.FixedIntervalMinutes+10) * time.Minute)
		probe := defaultWorkloadProbe()
		allocOpts := AllocateOptions{
			AllowBootstrap: req.AllowBootstrap,
			CountWriter:    s.chaosSystems,
		}
		for batchIdx, batch := range guidedSpecs {
			if !batchNeedsAutoAllocate(batch) {
				continue
			}
			traceID := uuid.NewString()
			result, allocErr := AllocateNamespaceForRestart(ctx, s.redis, pedestalItem.ContainerName, lockEndTime, traceID, probe, allocOpts)
			if allocErr != nil {
				hint := fmt.Sprintf("run `aegisctl inject guided --install --namespace %s<N>` to expand the pool, or pass --allow-bootstrap", pedestalItem.ContainerName)
				return nil, fmt.Errorf("auto-allocate batch %d for system %s: %w (hint: %s)", batchIdx, pedestalItem.ContainerName, allocErr, hint)
			}
			allocations[batchIdx] = allocatedSlot{namespace: result.Namespace, traceID: traceID, fresh: result.Fresh}
			for cfgIdx := range guidedSpecs[batchIdx] {
				if strings.TrimSpace(guidedSpecs[batchIdx][cfgIdx].Namespace) == "" {
					guidedSpecs[batchIdx][cfgIdx].Namespace = result.Namespace
				}
			}
		}
	}

	// Break the submit→restart-pedestal chicken-and-egg: on a first run,
	// the target namespace doesn't exist yet (RestartPedestal hasn't run),
	// so guidedcli.BuildInjection's pod listing would 500. Pre-create any
	// missing namespaces now; RestartPedestal helm-installs into them in a
	// few seconds. See github issues #91 item 1 and #92 item 1.
	for _, batch := range guidedSpecs {
		if err := ensureGuidedNamespaces(ctx, pedestalItem.ContainerName, batch, s.chaosSystems); err != nil {
			return nil, fmt.Errorf("failed to register guided namespaces with chaos-system %s: %w", pedestalItem.ContainerName, err)
		}
	}

	processedItems := make([]injectionProcessItem, 0, len(guidedSpecs))
	var parseWarnings []string
	for i := range guidedSpecs {
		alloc, hasAlloc := allocations[i]
		// Fresh bootstrapped slots have no workload yet, so the submit-time
		// guidedcli.BuildInjection inside parseBatchGuidedSpecs would fail
		// at `app X not found in namespace Y` because pod listing returns
		// empty. Build the item directly from the request-supplied fields
		// — system-type sanity check has already been done implicitly by
		// the allocator (it ran against the pedestal's system), and
		// groundtruth dedup warnings don't apply since fresh slots can't
		// collide with other batches in the same submit (each gets its
		// own ns).
		if hasAlloc && alloc.fresh {
			item := buildFreshSlotItem(i, guidedSpecs[i])
			item.allocatedNamespace = alloc.namespace
			item.preallocTraceID = alloc.traceID
			processedItems = append(processedItems, item)
			continue
		}
		item, warning, err := parseBatchGuidedSpecs(ctx, pedestalItem.ContainerName, i, guidedSpecs[i])
		if err != nil {
			return nil, fmt.Errorf("failed to parse guided spec batch %d: %w", i, err)
		}
		if hasAlloc {
			item.allocatedNamespace = alloc.namespace
			item.preallocTraceID = alloc.traceID
		}
		if warning != "" {
			parseWarnings = append(parseWarnings, warning)
		} else {
			processedItems = append(processedItems, *item)
		}
	}

	uniqueItems, duplicatedInRequest, alreadyExisted, err := s.removeDuplicated(processedItems)
	if err != nil {
		return nil, fmt.Errorf("failed to remove duplicated batches: %w", err)
	}

	var warnings *InjectionWarnings
	if len(parseWarnings) > 0 || len(duplicatedInRequest) > 0 || len(alreadyExisted) > 0 {
		warnings = &InjectionWarnings{
			DuplicateServicesInBatch:  parseWarnings,
			DuplicateBatchesInRequest: duplicatedInRequest,
			BatchesExistInDatabase:    alreadyExisted,
		}
	}

	if len(req.Algorithms) > 0 {
		refs := make([]*dto.ContainerRef, 0, len(req.Algorithms))
		for i := range req.Algorithms {
			refs = append(refs, &req.Algorithms[i].ContainerRef)
		}

		algorithmVersionsResults, err := s.containers.ResolveContainerVersions(refs, consts.ContainerTypeAlgorithm, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to map container refs to versions: %w", err)
		}

		var algorithmVersionItems []dto.ContainerVersionItem
		for i := range req.Algorithms {
			spec := &req.Algorithms[i]
			algorithmVersion, exists := algorithmVersionsResults[&spec.ContainerRef]
			if !exists {
				return nil, fmt.Errorf("algorithm version not found for %v", spec)
			}

			algorithmVersionItem := dto.NewContainerVersionItem(&algorithmVersion)
			envVars, err := s.containers.ListContainerVersionEnvVars(spec.EnvVars, &algorithmVersion)
			if err != nil {
				return nil, fmt.Errorf("failed to list algorithm env vars: %w", err)
			}

			algorithmVersionItem.EnvVars = envVars
			algorithmVersionItems = append(algorithmVersionItems, algorithmVersionItem)
		}

		if len(algorithmVersionItems) > 0 {
			if err := s.redis.SetHashField(ctx, consts.InjectionAlgorithmsKey, groupID, algorithmVersionItems); err != nil {
				return nil, fmt.Errorf("failed to store injection algorithms: %w", err)
			}
		}
	}

	injectionItems := make([]SubmitInjectionItem, 0, len(uniqueItems))
	for _, item := range uniqueItems {
		injectPayload := map[string]any{
			consts.InjectBenchmark:     benchmarkVersionItem,
			consts.InjectPreDuration:   consts.FixedNormalWindowMinutes,
			consts.InjectLabels:        req.Labels,
			consts.InjectSystem:        pedestalItem.ContainerName,
			consts.InjectGuidedConfigs: item.guidedConfigs,
		}
		payload := map[string]any{
			consts.RestartPedestal:      pedestalItem,
			consts.RestartHelmConfig:    helmConfig,
			consts.RestartIntarval:      consts.FixedIntervalMinutes,
			consts.RestartFaultDuration: consts.FixedAbnormalWindowMinutes,
			consts.RestartInjectPayload: injectPayload,
			consts.RestartSkipInstall:   req.SkipRestartPedestal,
		}

		// #156: when the guided config names a namespace, treat it as a hard
		// constraint on RestartPedestal. Without this the runtime falls back
		// to `monitor.GetNamespaceToRestart`, which picks the first enabled
		// namespace matching the system's NsPattern — silently rerouting a
		// `sockshop14`-targeted submit to `sockshop0`. We thread a single
		// required namespace per task; within one guided batch all configs
		// must share the same namespace anyway (they run under one lock), so
		// the first non-empty value is authoritative.
		if requiredNs := firstGuidedNamespace(item.guidedConfigs); requiredNs != "" {
			payload[consts.RestartRequiredNamespace] = requiredNs
		}

		task := &dto.UnifiedTask{
			Type:        consts.TaskTypeRestartPedestal,
			Immediate:   false,
			ExecuteTime: item.executeTime.Unix(),
			Payload:     payload,
			GroupID:     groupID,
			ProjectID:   *projectID,
			UserID:      userID,
			State:       consts.TaskPending,
			Extra: map[consts.TaskExtra]any{
				consts.TaskExtraInjectionAlgorithms: len(req.Algorithms),
			},
		}
		// #166: AutoAllocate locked the chosen namespace under
		// item.preallocTraceID at submit time. Pin the task's TraceID to that
		// value so monitor.AcquireNamespaceForRestart at runtime treats it
		// as a same-owner re-acquire (idempotent) instead of seeing a
		// foreign-owner busy lock. Empty preallocTraceID falls through to
		// SubmitTaskWithDB's normal uuid generation.
		if item.preallocTraceID != "" {
			task.TraceID = item.preallocTraceID
		}
		task.SetGroupCtx(ctx)

		if err := common.SubmitTaskWithDB(ctx, db, s.redis, task); err != nil {
			return nil, fmt.Errorf("failed to submit fault injection task: %w", err)
		}

		injectionItems = append(injectionItems, SubmitInjectionItem{
			Index:              item.index,
			TraceID:            task.TraceID,
			TaskID:             task.TaskID,
			AllocatedNamespace: item.allocatedNamespace,
		})
	}

	sort.Slice(injectionItems, func(i, j int) bool { return injectionItems[i].Index < injectionItems[j].Index })
	return &SubmitInjectionResp{
		GroupID:       groupID,
		Items:         injectionItems,
		OriginalCount: len(processedItems),
		Warnings:      warnings,
	}, nil
}

func (s *Service) SubmitDatapackBuilding(ctx context.Context, req *SubmitDatapackBuildingReq, groupID string, userID int, projectID *int) (*SubmitDatapackBuildingResp, error) {
	if req == nil {
		return nil, fmt.Errorf("submit datapack building request is nil")
	}
	db := s.repo.db

	if projectID == nil {
		project, err := s.repo.resolveProject(req.ProjectName)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("%w: project %s not found", consts.ErrNotFound, req.ProjectName)
			}
			return nil, fmt.Errorf("failed to get project: %w", err)
		}
		projectID = &project.ID
	}

	refs := make([]*dto.ContainerRef, 0, len(req.Specs))
	for i := range req.Specs {
		refs = append(refs, &req.Specs[i].Benchmark.ContainerRef)
	}

	benchmarkVersionResults, err := s.containers.ResolveContainerVersions(refs, consts.ContainerTypeBenchmark, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to map container refs to versions: %w", err)
	}

	var allBuildingItems []SubmitBuildingItem
	for idx, spec := range req.Specs {
		datapacks, datasetVersionID, err := s.ResolveDatapacks(spec.Datapack, spec.Dataset, userID, consts.TaskTypeBuildDatapack)
		if err != nil {
			return nil, fmt.Errorf("failed to extract datapacks: %w", err)
		}

		benchmarkVersion, exists := benchmarkVersionResults[refs[idx]]
		if !exists {
			return nil, fmt.Errorf("benchmark version not found for %v", spec.Benchmark)
		}

		benchmarkVersionItem := dto.NewContainerVersionItem(&benchmarkVersion)
		envVars, err := s.containers.ListContainerVersionEnvVars(spec.Benchmark.EnvVars, &benchmarkVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to list benchmark env vars: %w", err)
		}
		benchmarkVersionItem.EnvVars = envVars

		for _, datapack := range datapacks {
			if datapack.StartTime == nil || datapack.EndTime == nil {
				return nil, fmt.Errorf("datapack %s does not have valid start_time and end_time", datapack.Name)
			}

			payload := map[string]any{
				consts.BuildBenchmark:        benchmarkVersionItem,
				consts.BuildDatapack:         dto.NewInjectionItem(&datapack),
				consts.BuildDatasetVersionID: datasetVersionID,
				consts.BuildLabels:           req.Labels,
			}

			task := &dto.UnifiedTask{
				Type:      consts.TaskTypeBuildDatapack,
				Immediate: true,
				Payload:   payload,
				GroupID:   groupID,
				ProjectID: *projectID,
				UserID:    userID,
				State:     consts.TaskPending,
			}
			task.SetGroupCtx(ctx)

			if err := common.SubmitTaskWithDB(ctx, db, s.redis, task); err != nil {
				return nil, fmt.Errorf("failed to submit datapack building task: %w", err)
			}

			allBuildingItems = append(allBuildingItems, SubmitBuildingItem{
				Index:   idx,
				TraceID: task.TraceID,
				TaskID:  task.TaskID,
			})
		}
	}

	return &SubmitDatapackBuildingResp{
		GroupID: groupID,
		Items:   allBuildingItems,
	}, nil
}

func (s *Service) ListInjections(_ context.Context, req *ListInjectionReq) (*dto.ListResp[InjectionResp], error) {
	limit, offset := req.ToGormParams()
	injections, total, err := s.repo.listInjectionsView(limit, offset, req.ToFilterOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to list injections: %w", err)
	}

	items := make([]InjectionResp, 0, len(injections))
	for _, injection := range injections {
		items = append(items, *NewInjectionResp(&injection))
	}

	return &dto.ListResp[InjectionResp]{
		Items:      items,
		Pagination: req.ConvertToPaginationInfo(total),
	}, nil
}

func (s *Service) GetInjection(_ context.Context, id int) (*InjectionDetailResp, error) {
	injection, err := s.repo.getInjectionWithLabels(id)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			return nil, fmt.Errorf("%w: injection id: %d", consts.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get injection: %w", err)
	}
	return NewInjectionDetailResp(injection), nil
}

func (s *Service) ManageLabels(_ context.Context, req *ManageInjectionLabelReq, id int) (*InjectionResp, error) {
	var managedInjection *model.FaultInjection
	err := s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		injection, err := repo.loadInjection(id)
		if err != nil {
			if errors.Is(err, consts.ErrNotFound) {
				return fmt.Errorf("%w: injection id: %d", consts.ErrNotFound, id)
			}
			return fmt.Errorf("failed to get injection: %w", err)
		}

		if len(req.AddLabels) > 0 {
			labels, err := s.labels.CreateOrUpdateLabelsFromItems(tx, req.AddLabels, consts.InjectionCategory)
			if err != nil {
				return fmt.Errorf("failed to create or update labels: %w", err)
			}
			labelIDs := make([]int, 0, len(labels))
			for _, label := range labels {
				labelIDs = append(labelIDs, label.ID)
			}
			if err := repo.addInjectionLabels(injection.ID, labelIDs); err != nil {
				return fmt.Errorf("failed to add injection labels: %w", err)
			}
		}

		if len(req.RemoveLabels) > 0 {
			labelIDs, err := repo.listInjectionLabelIDsByKeys(injection.ID, req.RemoveLabels)
			if err != nil {
				return fmt.Errorf("failed to find label ids by keys: %w", err)
			}
			if len(labelIDs) > 0 {
				if err := repo.clearInjectionLabels([]int{id}, labelIDs); err != nil {
					return fmt.Errorf("failed to clear injection labels: %w", err)
				}
				if err := repo.batchDecreaseLabelUsages(labelIDs, 1); err != nil {
					return fmt.Errorf("failed to decrease label usage counts: %w", err)
				}
			}
		}

		managedInjection, err = repo.getInjectionWithLabels(id)
		if err != nil {
			return fmt.Errorf("failed to reload injection labels: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return NewInjectionResp(managedInjection), nil
}

func (s *Service) BatchManageLabels(_ context.Context, req *BatchManageInjectionLabelReq) (*BatchManageInjectionLabelResp, error) {
	resp := &BatchManageInjectionLabelResp{
		FailedItems:  []string{},
		SuccessItems: []InjectionResp{},
	}
	if len(req.Items) == 0 {
		return resp, nil
	}

	return resp, s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		allInjectionIDs := make([]int, 0, len(req.Items))
		operationMap := make(map[int]*InjectionLabelOperation, len(req.Items))
		for i := range req.Items {
			item := &req.Items[i]
			allInjectionIDs = append(allInjectionIDs, item.InjectionID)
			operationMap[item.InjectionID] = item
		}

		foundIDMap, err := repo.loadExistingInjectionsByID(allInjectionIDs)
		if err != nil {
			return fmt.Errorf("failed to list injections: %w", err)
		}

		validIDs := make([]int, 0, len(foundIDMap))
		for _, id := range allInjectionIDs {
			if _, found := foundIDMap[id]; !found {
				resp.FailedItems = append(resp.FailedItems, fmt.Sprintf("Injection ID %d not found", id))
				resp.FailedCount++
				delete(operationMap, id)
			} else {
				validIDs = append(validIDs, id)
			}
		}
		if len(validIDs) == 0 {
			return fmt.Errorf("no valid injection IDs found")
		}

		allAddLabels := make([]dto.LabelItem, 0)
		allRemoveLabels := make([]dto.LabelItem, 0)
		labelKeySet := make(map[string]bool)
		for _, op := range operationMap {
			for _, label := range op.AddLabels {
				key := label.Key + ":" + label.Value
				if !labelKeySet[key] {
					labelKeySet[key] = true
					allAddLabels = append(allAddLabels, label)
				}
			}
			for _, label := range op.RemoveLabels {
				key := label.Key + ":" + label.Value
				if !labelKeySet[key] {
					labelKeySet[key] = true
					allRemoveLabels = append(allRemoveLabels, label)
				}
			}
		}

		var labelMap map[string]int
		if len(allAddLabels) > 0 {
			labels, err := s.labels.CreateOrUpdateLabelsFromItems(tx, allAddLabels, consts.InjectionCategory)
			if err != nil {
				return fmt.Errorf("failed to create or update labels: %w", err)
			}
			labelMap = make(map[string]int, len(labels))
			for _, label := range labels {
				labelMap[label.Key+":"+label.Value] = label.ID
			}
		}

		var removeLabelMap map[string]int
		if len(allRemoveLabels) > 0 {
			labelConditions := make([]map[string]string, 0, len(allRemoveLabels))
			for _, item := range allRemoveLabels {
				labelConditions = append(labelConditions, map[string]string{"key": item.Key, "value": item.Value})
			}
			removeLabelMap, err = repo.loadInjectionLabelIDsByItems(labelConditions, consts.InjectionCategory)
			if err != nil {
				return fmt.Errorf("failed to find labels to remove: %w", err)
			}
		}

		for _, injectionID := range validIDs {
			op := operationMap[injectionID]
			if len(op.AddLabels) > 0 {
				labelIDsToAdd := make([]int, 0, len(op.AddLabels))
				for _, label := range op.AddLabels {
					if labelID, exists := labelMap[label.Key+":"+label.Value]; exists {
						labelIDsToAdd = append(labelIDsToAdd, labelID)
					}
				}
				if len(labelIDsToAdd) > 0 {
					if err := repo.addInjectionLabels(injectionID, labelIDsToAdd); err != nil {
						resp.FailedItems = append(resp.FailedItems, fmt.Sprintf("Injection ID %d: failed to add labels - %s", injectionID, err.Error()))
						resp.FailedCount++
						delete(foundIDMap, injectionID)
						continue
					}
				}
			}

			if len(op.RemoveLabels) > 0 && removeLabelMap != nil {
				labelIDsToRemove := make([]int, 0, len(op.RemoveLabels))
				for _, label := range op.RemoveLabels {
					if labelID, exists := removeLabelMap[label.Key+":"+label.Value]; exists {
						labelIDsToRemove = append(labelIDsToRemove, labelID)
					}
				}
				if len(labelIDsToRemove) > 0 {
					if err := repo.clearInjectionLabels([]int{injectionID}, labelIDsToRemove); err != nil {
						resp.FailedItems = append(resp.FailedItems, fmt.Sprintf("Injection ID %d: failed to remove labels - %s", injectionID, err.Error()))
						resp.FailedCount++
						delete(foundIDMap, injectionID)
						continue
					}
				}
			}
		}

		if len(foundIDMap) > 0 {
			successIDs := make([]int, 0, len(foundIDMap))
			for id := range foundIDMap {
				successIDs = append(successIDs, id)
			}
			updatedInjections, err := repo.listFaultInjectionsByIDWithLabels(successIDs)
			if err != nil {
				return fmt.Errorf("failed to fetch updated injections: %w", err)
			}
			for i := range updatedInjections {
				injection := &updatedInjections[i]
				resp.SuccessItems = append(resp.SuccessItems, *NewInjectionResp(injection))
				resp.SuccessCount++
			}
		}

		return nil
	})
}

func (s *Service) BatchDelete(ctx context.Context, req *BatchDeleteInjectionReq) error {
	if len(req.IDs) > 0 {
		return s.batchDeleteByIDs(req.IDs)
	}
	return s.batchDeleteByLabels(req.Labels)
}

func (s *Service) Clone(_ context.Context, id int, req *CloneInjectionReq) (*InjectionDetailResp, error) {
	original, err := s.repo.loadInjection(id)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			return nil, fmt.Errorf("%w: injection id: %d", consts.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get injection: %w", err)
	}

	cloned := &model.FaultInjection{
		Name:          req.Name,
		FaultType:     original.FaultType,
		Category:      original.Category,
		Description:   original.Description,
		DisplayConfig: original.DisplayConfig,
		EngineConfig:  original.EngineConfig,
		Groundtruths:  original.Groundtruths,
		PreDuration:   original.PreDuration,
		StartTime:     original.StartTime,
		EndTime:       original.EndTime,
		BenchmarkID:   original.BenchmarkID,
		PedestalID:    original.PedestalID,
		State:         consts.DatapackInitial,
		Status:        consts.CommonEnabled,
	}

	err = s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		if err := repo.createInjectionRecord(cloned); err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: injection with name %s already exists", consts.ErrAlreadyExists, cloned.Name)
			}
			return fmt.Errorf("failed to create injection: %w", err)
		}
		if len(req.Labels) > 0 {
			labels, err := s.labels.CreateOrUpdateLabelsFromItems(tx, req.Labels, consts.InjectionCategory)
			if err != nil {
				return fmt.Errorf("failed to create or update labels: %w", err)
			}
			labelIDs := make([]int, 0, len(labels))
			for _, label := range labels {
				labelIDs = append(labelIDs, label.ID)
			}
			if err := repo.addInjectionLabels(cloned.ID, labelIDs); err != nil {
				return fmt.Errorf("failed to add injection labels: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	cloned, err = s.repo.getInjectionWithLabels(cloned.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cloned injection labels: %w", err)
	}
	return NewInjectionDetailResp(cloned), nil
}

func (s *Service) GetLogs(ctx context.Context, id int) (*InjectionLogsResp, error) {
	injection, err := s.repo.loadInjection(id)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			return nil, fmt.Errorf("%w: injection id: %d", consts.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get injection: %w", err)
	}

	resp := &InjectionLogsResp{InjectionID: id, Logs: []string{}}
	if injection.TaskID == nil {
		return resp, nil
	}

	resp.TaskID = *injection.TaskID
	task, taskErr := s.repo.loadTask(*injection.TaskID)
	if taskErr != nil {
		return resp, nil
	}

	lokiCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	logEntries, lokiErr := s.lokiClient.QueryJobLogs(lokiCtx, *injection.TaskID, loki.QueryOpts{
		Start:     task.CreatedAt,
		Direction: "forward",
	})
	if lokiErr != nil {
		return resp, nil
	}
	for _, entry := range logEntries {
		resp.Logs = append(resp.Logs, entry.Line)
	}
	return resp, nil
}

func (s *Service) GetDatapackFilename(_ context.Context, id int) (string, error) {
	injection, err := s.repo.loadInjection(id)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("%w: injection id: %d", consts.ErrNotFound, id)
		}
		return "", fmt.Errorf("failed to get injection: %w", err)
	}
	if injection.State < consts.DatapackBuildSuccess {
		return "", fmt.Errorf("datapack for injection id %d is not ready for download", id)
	}
	return injection.Name, nil
}

func (s *Service) DownloadDatapack(_ context.Context, zipWriter *zip.Writer, excludeRules []utils.ExculdeRule, id int) error {
	if zipWriter == nil {
		return fmt.Errorf("zip writer cannot be nil")
	}
	injection, err := s.getReadyDatapack(id)
	if err != nil {
		return err
	}
	if err := s.store.Package(zipWriter, injection.Name, excludeRules); err != nil {
		return fmt.Errorf("failed to package injection to zip: %w", err)
	}
	return nil
}

func (s *Service) GetDatapackFiles(_ context.Context, id int, baseURL string) (*DatapackFilesResp, error) {
	injection, err := s.getReadyDatapack(id)
	if err != nil {
		return nil, err
	}
	resp, err := s.store.BuildFileTree(injection.Name, baseURL, id)
	if err != nil {
		return nil, fmt.Errorf("failed to build file tree: %w", err)
	}
	return resp, nil
}

func (s *Service) DownloadDatapackFile(_ context.Context, id int, filePath string) (string, string, int64, io.ReadSeekCloser, error) {
	injection, err := s.getReadyDatapack(id)
	if err != nil {
		return "", "", 0, nil, err
	}
	return s.store.OpenFile(injection.Name, filePath)
}

func (s *Service) QueryDatapackFile(ctx context.Context, id int, filePath string) (string, int64, io.ReadCloser, error) {
	return s.queryDatapackFileContent(ctx, id, filePath)
}

func (s *Service) GetDatapackSchema(ctx context.Context, id int) (*DatapackSchemaResp, error) {
	return s.getDatapackSchema(ctx, id)
}

func (s *Service) QueryDatapack(ctx context.Context, id int, userSQL string) (io.ReadCloser, error) {
	return s.runDatapackQuery(ctx, id, userSQL)
}

func (s *Service) UpdateGroundtruth(_ context.Context, id int, req *UpdateGroundtruthReq) error {
	if _, err := s.repo.loadInjection(id); err != nil {
		return err
	}
	return s.repo.updateGroundtruth(id, req.Groundtruths, consts.GroundtruthSourceManual)
}

func (s *Service) CreateInjectionRecord(_ context.Context, req *RuntimeCreateInjectionReq) (*dto.InjectionItem, error) {
	if req == nil {
		return nil, fmt.Errorf("runtime create injection request is nil")
	}
	if req.Name == "" || req.TaskID == "" {
		return nil, fmt.Errorf("%w: name and task_id are required", consts.ErrBadRequest)
	}

	var created *model.FaultInjection
	err := s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		injection := &model.FaultInjection{
			Name:              req.Name,
			Source:            consts.DatapackSourceInjection,
			FaultType:         req.FaultType,
			Category:          req.Category,
			Description:       req.Description,
			DisplayConfig:     utils.StringPtr(req.DisplayConfig),
			EngineConfig:      req.EngineConfig,
			Groundtruths:      req.Groundtruths,
			GroundtruthSource: req.GroundtruthSource,
			PreDuration:       req.PreDuration,
			TaskID:            utils.StringPtr(req.TaskID),
			BenchmarkID:       req.BenchmarkID,
			PedestalID:        req.PedestalID,
			State:             req.State,
			Status:            consts.CommonEnabled,
		}

		if err := repo.createInjectionRecord(injection); err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: injection %s already exists", consts.ErrAlreadyExists, req.Name)
			}
			return err
		}

		if len(req.Labels) > 0 {
			createdLabels, err := s.labels.CreateOrUpdateLabelsFromItems(tx, req.Labels, consts.InjectionCategory)
			if err != nil {
				return fmt.Errorf("failed to create or update labels: %w", err)
			}

			labelIDs := make([]int, 0, len(createdLabels))
			for _, label := range createdLabels {
				labelIDs = append(labelIDs, label.ID)
			}

			if err := repo.addInjectionLabels(injection.ID, labelIDs); err != nil {
				return fmt.Errorf("failed to add injection labels: %w", err)
			}
		}

		created = injection
		return nil
	})
	if err != nil {
		return nil, err
	}

	item := dto.NewInjectionItem(created)
	return &item, nil
}

func (s *Service) UpdateInjectionState(_ context.Context, req *RuntimeUpdateInjectionStateReq) error {
	if req == nil {
		return fmt.Errorf("runtime update injection state request is nil")
	}
	if req.Name == "" {
		return fmt.Errorf("%w: name is required", consts.ErrBadRequest)
	}

	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		injection, err := repo.findInjectionByName(req.Name, false)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: injection %s not found", consts.ErrNotFound, req.Name)
			}
			return err
		}
		return repo.updateInjectionFields(injection.ID, map[string]any{"state": req.State})
	})
}

func (s *Service) UpdateInjectionTimestamps(_ context.Context, req *RuntimeUpdateInjectionTimestampReq) (*dto.InjectionItem, error) {
	if req == nil {
		return nil, fmt.Errorf("runtime update injection timestamp request is nil")
	}
	if req.Name == "" {
		return nil, fmt.Errorf("%w: name is required", consts.ErrBadRequest)
	}

	var updated *model.FaultInjection
	err := s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		injection, err := repo.findInjectionByName(req.Name, false)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: injection %s not found", consts.ErrNotFound, req.Name)
			}
			return err
		}
		if err := repo.updateInjectionFields(injection.ID, map[string]any{
			"start_time": req.StartTime,
			"end_time":   req.EndTime,
		}); err != nil {
			return err
		}

		reloaded, err := repo.loadInjection(injection.ID)
		if err != nil {
			return err
		}
		updated = reloaded
		return nil
	})
	if err != nil {
		return nil, err
	}

	item := dto.NewInjectionItem(updated)
	return &item, nil
}

func (s *Service) UploadDatapack(_ context.Context, req *UploadDatapackReq, file io.Reader, fileSize int64) (*UploadDatapackResp, error) {
	_ = fileSize

	labels, err := req.ParseLabels()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", consts.ErrBadRequest, err.Error())
	}

	groundtruths, err := req.ParseGroundtruths()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", consts.ErrBadRequest, err.Error())
	}

	existing, _ := s.repo.findInjectionByName(req.Name, false)
	if existing != nil {
		return nil, fmt.Errorf("%w: injection with name %s already exists", consts.ErrAlreadyExists, req.Name)
	}

	tmpFile, err := s.store.CreateUploadTempFile()
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = s.store.Remove(tmpPath) }()

	if _, err := io.Copy(tmpFile, file); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("failed to save uploaded file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close uploaded file: %w", err)
	}

	if err := s.store.ValidateArchive(tmpPath); err != nil {
		return nil, fmt.Errorf("%w: %s", consts.ErrBadRequest, err.Error())
	}

	targetDir, err := s.store.EnsureDatapackDirAvailable(req.Name)
	if err != nil {
		return nil, err
	}
	if err := s.store.ExtractArchive(tmpPath, targetDir); err != nil {
		_ = s.store.RemoveAll(targetDir)
		return nil, fmt.Errorf("failed to extract archive: %w", err)
	}

	groundtruthSource := ""
	if len(groundtruths) > 0 {
		groundtruthSource = consts.GroundtruthSourceManual
	} else {
		groundtruths = s.store.ExtractGroundtruths(targetDir)
		if len(groundtruths) > 0 {
			groundtruthSource = consts.GroundtruthSourceImported
		}
	}

	category := chaos.SystemType("")
	if req.Category != "" {
		category = chaos.SystemType(req.Category)
	}

	injection := &model.FaultInjection{
		Name:              req.Name,
		Source:            consts.DatapackSourceManual,
		FaultType:         chaos.ChaosType(0),
		Category:          category,
		Description:       req.Description,
		EngineConfig:      "",
		Groundtruths:      groundtruths,
		GroundtruthSource: groundtruthSource,
		PreDuration:       0,
		BenchmarkID:       nil,
		PedestalID:        nil,
		State:             consts.DatapackBuildSuccess,
		Status:            consts.CommonEnabled,
	}

	err = s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		if err := repo.createInjectionRecord(injection); err != nil {
			return err
		}

		if len(labels) > 0 {
			createdLabels, err := s.labels.CreateOrUpdateLabelsFromItems(tx, labels, consts.InjectionCategory)
			if err != nil {
				return fmt.Errorf("failed to create or update labels: %w", err)
			}

			labelIDs := make([]int, 0, len(createdLabels))
			for _, label := range createdLabels {
				labelIDs = append(labelIDs, label.ID)
			}

			if err := repo.addInjectionLabels(injection.ID, labelIDs); err != nil {
				return fmt.Errorf("failed to add injection labels: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		_ = s.store.RemoveAll(targetDir)
		return nil, err
	}

	return &UploadDatapackResp{
		ID:   injection.ID,
		Name: injection.Name,
	}, nil
}

func (s *Service) getReadyDatapack(id int) (*model.FaultInjection, error) {
	injection, err := s.repo.loadInjection(id)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: injection id: %d", consts.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get injection: %w", err)
	}
	if injection.State < consts.DatapackBuildSuccess {
		return nil, fmt.Errorf("datapack %d is not ready", id)
	}
	return injection, nil
}

func (s *Service) GetReadyDatapackName(_ context.Context, id int) (string, error) {
	injection, err := s.getReadyDatapack(id)
	if err != nil {
		return "", err
	}
	return injection.Name, nil
}

func (s *Service) batchDeleteByIDs(injectionIDs []int) error {
	if len(injectionIDs) == 0 {
		return nil
	}
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		return repo.deleteInjectionsCascade(injectionIDs)
	})
}

func (s *Service) batchDeleteByLabels(labelItems []dto.LabelItem) error {
	if len(labelItems) == 0 {
		return nil
	}
	labelConditions := make([]map[string]string, 0, len(labelItems))
	for _, item := range labelItems {
		labelConditions = append(labelConditions, map[string]string{"key": item.Key, "value": item.Value})
	}
	injectionIDs, err := s.repo.listInjectionIDsByLabelConditions(labelConditions)
	if err != nil {
		return fmt.Errorf("failed to list injection ids by labels: %w", err)
	}
	return s.batchDeleteByIDs(injectionIDs)
}

// CancelInjection cascades cancellation through to the task that backs the
// injection. The DB write + redis eviction + chaos CRD delete all happen
// inside task.Service.CancelTask; this method just resolves injection_id →
// task_id and surfaces the underlying outcome.
//
// Contract:
//   - injection not found → wrapped consts.ErrNotFound
//   - injection has no associated task (already detached / never submitted) →
//     wrapped consts.ErrBadRequest
//   - task already terminal → response carries the terminal state with no error
func (s *Service) CancelInjection(ctx context.Context, injectionID int) (*CancelInjectionResp, error) {
	injection, err := s.repo.loadInjection(injectionID)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			return nil, fmt.Errorf("%w: injection id: %d", consts.ErrNotFound, injectionID)
		}
		return nil, fmt.Errorf("failed to load injection: %w", err)
	}
	if injection.TaskID == nil || *injection.TaskID == "" {
		return nil, fmt.Errorf("%w: injection %d has no associated task to cancel", consts.ErrBadRequest, injectionID)
	}
	if s.taskCanceller == nil {
		return nil, fmt.Errorf("task canceller is not wired")
	}

	result, err := s.taskCanceller.CancelTask(ctx, *injection.TaskID)
	if err != nil {
		return nil, err
	}
	return &CancelInjectionResp{
		InjectionID:       injectionID,
		TaskID:            result.TaskID,
		State:             result.State,
		Message:           result.Message,
		DeletedPodChaos:   result.DeletedPodChaos,
		RemovedRedisTasks: result.RemovedRedisTasks,
	}, nil
}

func splitLabelCondition(item string) [2]string {
	parts := strings.SplitN(item, ":", 2)
	if len(parts) == 1 {
		return [2]string{parts[0], ""}
	}
	return [2]string{parts[0], parts[1]}
}
