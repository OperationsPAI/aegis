package chaossystem

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/dto"
	etcd "aegis/platform/etcd"
	"aegis/platform/model"
	"aegis/core/orchestrator/common"
	"aegis/boot/seed"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/sirupsen/logrus"
)

// enumerateCandidatesFn is the indirection used by ListInjectCandidates so
// tests can inject a fixture without standing up a fake k8s API. Defaults to
// the real in-process enumerator from chaos-experiment.
var enumerateCandidatesFn = guidedcli.EnumerateAllCandidates

// systemField is an internal enum of the injection.system.<name>.<field>
// suffixes we manage. Keeping this tight (instead of free-form map keys)
// prevents typos from slipping through at compile time.
type systemField string

const (
	fieldCount          systemField = "count"
	fieldNsPattern      systemField = "ns_pattern"
	fieldExtractPattern systemField = "extract_pattern"
	fieldDisplayName    systemField = "display_name"
	fieldAppLabelKey    systemField = "app_label_key"
	fieldIsBuiltin      systemField = "is_builtin"
	fieldStatus         systemField = "status"
)

// allSystemFields is the canonical ordering used when seeding a new system.
// The first entry is treated as the "anchor" row for ID/timestamp reporting.
func allSystemFields() []systemField {
	return []systemField{
		fieldCount,
		fieldNsPattern,
		fieldExtractPattern,
		fieldDisplayName,
		fieldAppLabelKey,
		fieldIsBuiltin,
		fieldStatus,
	}
}

// systemKeyPrefix is the etcd / dynamic_config prefix for one system.
func systemKeyPrefix(name string) string {
	return "injection.system." + name + "."
}

// systemKey formats the fully-qualified key for a single field.
func systemKey(name string, field systemField) string {
	return systemKeyPrefix(name) + string(field)
}

// Service is the HTTP-facing service for chaossystem. Reads come from Viper
// (the etcd-mirrored cache); writes go directly to etcd and are recorded in
// config_histories. The systems table is gone.
type Service struct {
	repo *Repository
	etcd chaosSystemEtcd
}

type chaosSystemEtcd interface {
	Get(ctx context.Context, key string) (string, error)
	Put(ctx context.Context, key, value string, ttl time.Duration) error
}

func NewService(repo *Repository, etcdGw *etcd.Gateway) *Service {
	// The etcd gateway is the write path for every CRUD mutation. Failing
	// loud here keeps a nil-passing caller from deferring the crash to the
	// first `s.etcd.Put(...)` deep inside a request handler.
	if etcdGw == nil {
		panic("chaossystem.NewService: etcd gateway is required")
	}
	return &Service{repo: repo, etcd: etcdGw}
}

func normalizeAppLabelKey(key string) string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return "app"
	}
	return trimmed
}

// ListSystems returns every system known to etcd/Viper, paginated.
func (s *Service) ListSystems(_ context.Context, req *ListChaosSystemReq) (*dto.ListResp[ChaosSystemResp], error) {
	configs, err := s.repo.ListSystemConfigs()
	if err != nil {
		return nil, err
	}
	anchors := buildAnchorIndex(configs)

	cfgMap := config.GetChaosSystemConfigManager().GetAll()
	names := make([]string, 0, len(cfgMap))
	for name := range cfgMap {
		// Ignore systems that have been tombstoned (status == CommonDeleted).
		if !isSystemVisible(cfgMap[name]) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	total := int64(len(names))
	limit, offset := req.ToGormParams()
	end := offset + limit
	if end > len(names) {
		end = len(names)
	}
	if offset > len(names) {
		offset = len(names)
	}
	pageNames := names[offset:end]

	items := make([]ChaosSystemResp, 0, len(pageNames))
	for _, name := range pageNames {
		anchor, ok := anchors[systemKey(name, fieldCount)]
		if !ok {
			// System exists in Viper but no dynamic_config row. Synthesize a
			// minimal anchor so responses stay well-formed.
			anchor = &model.DynamicConfig{Key: systemKey(name, fieldCount)}
		}
		view := newSystemView(anchor, cfgMap[name])
		items = append(items, *NewChaosSystemResp(view))
	}

	return &dto.ListResp[ChaosSystemResp]{
		Items:      items,
		Pagination: req.ConvertToPaginationInfo(total),
	}, nil
}

// GetSystem looks up a system by the anchor DynamicConfig row ID.
func (s *Service) GetSystem(_ context.Context, id int) (*ChaosSystemResp, error) {
	view, err := s.lookupByID(id)
	if err != nil {
		return nil, err
	}
	return NewChaosSystemResp(view), nil
}

// CreateSystem creates the 7 dynamic_config rows for a new system and
// publishes each initial value to etcd so the config watcher picks the new
// system up and calls chaos.RegisterSystem.
func (s *Service) CreateSystem(ctx context.Context, req *CreateChaosSystemReq) (*ChaosSystemResp, error) {
	if _, err := regexp.Compile(req.NsPattern); err != nil {
		return nil, fmt.Errorf("invalid ns_pattern regex: %w: %w", err, consts.ErrBadRequest)
	}
	if _, err := regexp.Compile(req.ExtractPattern); err != nil {
		return nil, fmt.Errorf("invalid extract_pattern regex: %w: %w", err, consts.ErrBadRequest)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("system name is required: %w", consts.ErrBadRequest)
	}

	// Reject duplicate creates: if any existing row exists under this prefix
	// we bail out so callers explicitly go through UpdateSystem instead.
	existing, err := s.repo.GetConfigByKey(systemKey(name, fieldCount))
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("system %s already exists: %w", name, consts.ErrAlreadyExists)
	}

	appLabelKey := normalizeAppLabelKey(req.AppLabelKey)
	defaults := map[systemField]string{
		fieldCount:          strconv.Itoa(req.Count),
		fieldNsPattern:      req.NsPattern,
		fieldExtractPattern: req.ExtractPattern,
		fieldDisplayName:    req.DisplayName,
		fieldAppLabelKey:    appLabelKey,
		fieldIsBuiltin:      strconv.FormatBool(req.IsBuiltin),
		fieldStatus:         strconv.Itoa(int(consts.CommonEnabled)),
	}
	descriptions := map[systemField]string{
		fieldCount:          fmt.Sprintf("Number of system %s to create", req.DisplayName),
		fieldNsPattern:      fmt.Sprintf("Namespace pattern for system %s instances", req.DisplayName),
		fieldExtractPattern: fmt.Sprintf("Extraction pattern for namespace prefix and number from %s instances", req.DisplayName),
		fieldDisplayName:    fmt.Sprintf("Human-readable display name for system %s", name),
		fieldAppLabelKey:    fmt.Sprintf("Kubernetes pod label key used to select %s workloads", req.DisplayName),
		fieldIsBuiltin:      fmt.Sprintf("Whether %s is a builtin benchmark system", req.DisplayName),
		fieldStatus:         fmt.Sprintf("Status of system %s (1=enabled, 0=disabled, -1=deleted)", req.DisplayName),
	}

	if req.Description != "" {
		descriptions[fieldCount] = req.Description
	}

	// Seed dynamic_config rows first so the etcd listener has validation
	// metadata to fall back on if it needs to re-seed from defaults later.
	// injection.system.* rows are Global-scoped so both producer and consumer
	// pick them up through the shared /rcabench/config/global/ watcher.
	for _, field := range allSystemFields() {
		cfg := &model.DynamicConfig{
			Key:          systemKey(name, field),
			DefaultValue: defaults[field],
			ValueType:    valueTypeForField(field),
			Scope:        consts.ConfigScopeGlobal,
			Category:     "injection.system." + string(field),
			Description:  descriptions[field],
		}
		if err := s.repo.CreateConfig(cfg); err != nil {
			return nil, err
		}
	}

	// Publish every value to etcd. The consumer watcher reconciles the
	// chaos-experiment registry on status/ns_pattern events.
	for _, field := range allSystemFields() {
		if err := s.publishKey(ctx, systemKey(name, field), defaults[field]); err != nil {
			return nil, fmt.Errorf("failed to publish %s to etcd: %w", field, err)
		}
	}

	common.InvalidateGlobalMetadataStoreCache()

	// The config manager reads Viper on demand; the consumer watch handler
	// will keep Viper in sync when the etcd event round-trips back.
	view, err := s.lookupByName(name)
	if err != nil {
		return nil, err
	}
	return NewChaosSystemResp(view), nil
}

// GetSystemChart returns the chart source bound to the system's pedestal
// ContainerVersion for the requested semver. When versionName is empty the
// highest-versioned active container_version wins. Returns ErrNotFound when
// the system has no pedestal container / no matching version / no helm
// config.
//
// Issue #190: the response is built from a fresh JOIN of container_versions
// × helm_configs × helm_config_values × parameter_configs every call, so a
// reseed-then-curl loop sees the latest helm_config_values.default_value
// without restarting the backend or re-rendering any cached configmap.
func (s *Service) GetSystemChart(_ context.Context, name, versionName string) (*SystemChartResp, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("system name is required: %w", consts.ErrBadRequest)
	}
	versionName = strings.TrimSpace(versionName)
	helm, version, err := s.repo.GetPedestalHelmConfigByName(name, versionName)
	if err != nil {
		return nil, err
	}
	if helm == nil || version == nil {
		return nil, fmt.Errorf("no active pedestal chart for system %s: %w", name, consts.ErrNotFound)
	}
	values := dto.NewHelmConfigItem(helm).GetValuesMap()
	if len(values) == 0 {
		values = nil
	}
	return &SystemChartResp{
		SystemName:  name,
		ChartName:   helm.ChartName,
		Version:     helm.Version,
		RepoURL:     helm.RepoURL,
		RepoName:    helm.RepoName,
		LocalPath:   helm.LocalPath,
		ValueFile:   helm.ValueFile,
		Values:      values,
		Checksum:    helm.Checksum,
		PedestalTag: version.Name,
	}, nil
}

// UpdateSystem mutates one or more injection.system.<name>.* keys via etcd
// and records a history entry per changed field.
func (s *Service) UpdateSystem(ctx context.Context, id int, req *UpdateChaosSystemReq) (*ChaosSystemResp, error) {
	view, err := s.lookupByID(id)
	if err != nil {
		return nil, err
	}

	changes := []struct {
		field systemField
		value string
	}{}
	if req.DisplayName != nil {
		changes = append(changes, struct {
			field systemField
			value string
		}{fieldDisplayName, *req.DisplayName})
	}
	if req.NsPattern != nil {
		if _, err := regexp.Compile(*req.NsPattern); err != nil {
			return nil, fmt.Errorf("invalid ns_pattern regex: %w: %w", err, consts.ErrBadRequest)
		}
		changes = append(changes, struct {
			field systemField
			value string
		}{fieldNsPattern, *req.NsPattern})
	}
	if req.ExtractPattern != nil {
		if _, err := regexp.Compile(*req.ExtractPattern); err != nil {
			return nil, fmt.Errorf("invalid extract_pattern regex: %w: %w", err, consts.ErrBadRequest)
		}
		changes = append(changes, struct {
			field systemField
			value string
		}{fieldExtractPattern, *req.ExtractPattern})
	}
	if req.AppLabelKey != nil {
		changes = append(changes, struct {
			field systemField
			value string
		}{fieldAppLabelKey, normalizeAppLabelKey(*req.AppLabelKey)})
	}
	if req.Count != nil {
		changes = append(changes, struct {
			field systemField
			value string
		}{fieldCount, strconv.Itoa(*req.Count)})
	}
	if req.Status != nil {
		// -1 (CommonDeleted) is the tombstone marker written by DeleteSystem.
		// Refuse it here so callers can't bypass the builtin guard / local
		// chaos.UnregisterSystem side-effect by sneaking a status flip in
		// through the generic update endpoint.
		if *req.Status == int(consts.CommonDeleted) {
			return nil, fmt.Errorf("status -1 is reserved for delete; use DELETE instead: %w", consts.ErrBadRequest)
		}
		if *req.Status != int(consts.CommonEnabled) && *req.Status != int(consts.CommonDisabled) {
			return nil, fmt.Errorf("status must be 0 (disabled) or 1 (enabled), got %d: %w", *req.Status, consts.ErrBadRequest)
		}
		// Builtin systems are immutable w.r.t. deletion (see DeleteSystem).
		// Mirror that guard for enable/disable so builtin benchmarks can't be
		// silently turned off by a status PUT.
		if view.Cfg.IsBuiltin {
			return nil, fmt.Errorf("cannot change status of builtin system %s: %w", view.Cfg.System, consts.ErrBadRequest)
		}
		changes = append(changes, struct {
			field systemField
			value string
		}{fieldStatus, strconv.Itoa(*req.Status)})
	}

	for _, change := range changes {
		if err := s.applyChange(ctx, view.Cfg.System, change.field, change.value); err != nil {
			return nil, err
		}
	}

	if req.Description != nil {
		// Description is a metadata-only field on the anchor row; it does not
		// need an etcd round-trip but we still write a history entry for
		// auditability.
		anchor, err := s.repo.GetConfigByKey(systemKey(view.Cfg.System, fieldCount))
		if err != nil {
			return nil, err
		}
		if anchor != nil {
			oldDesc := anchor.Description
			anchor.Description = *req.Description
			if err := s.saveAnchor(anchor); err != nil {
				return nil, err
			}
			_ = s.repo.WriteHistory(&model.ConfigHistory{
				ChangeType:  consts.ChangeTypeUpdate,
				ChangeField: consts.ChangeFieldDescription,
				OldValue:    oldDesc,
				NewValue:    *req.Description,
				ConfigID:    anchor.ID,
			})
		}
	}

	updated, err := s.lookupByID(id)
	if err != nil {
		return nil, err
	}
	return NewChaosSystemResp(updated), nil
}

// EnsureCountForNamespace bumps `injection.system.<system>.count` so that
// the requested namespace falls within the system's enumerated range,
// which is what `config.GetAllNamespaces()` exposes to the namespace
// monitor's AcquireLock validation. See #156: prior to this hook,
// `aegisctl inject guided --install --namespace sockshop14` created the
// workload but left count=1, so a subsequent submit's AcquireLock for
// `sockshop14` failed with "not found in current configuration" and the
// runtime silently fell back to the NsPattern pool.
//
// Idempotent: returns (false, nil) when the count is already large enough.
// Validates that the namespace actually matches the system's NsPattern /
// ExtractPattern before writing — a `sockshop14` request against a `ts`
// system rejects with an explicit mismatch error rather than corrupting
// counts.
func (s *Service) EnsureCountForNamespace(ctx context.Context, systemName, namespace string) (bool, error) {
	systemName = strings.TrimSpace(systemName)
	namespace = strings.TrimSpace(namespace)
	if systemName == "" {
		return false, fmt.Errorf("system name is required: %w", consts.ErrBadRequest)
	}
	if namespace == "" {
		return false, fmt.Errorf("namespace is required: %w", consts.ErrBadRequest)
	}

	view, err := s.lookupByName(systemName)
	if err != nil {
		return false, err
	}

	rx, err := regexp.Compile(view.Cfg.NsPattern)
	if err != nil {
		return false, fmt.Errorf("invalid ns_pattern for system %s: %w", systemName, err)
	}
	if !rx.MatchString(namespace) {
		return false, fmt.Errorf("namespace %q does not match system %s NsPattern %q: %w",
			namespace, systemName, view.Cfg.NsPattern, consts.ErrBadRequest)
	}

	idx, err := view.Cfg.ExtractNsNumber(namespace)
	if err != nil {
		return false, err
	}
	needed := idx + 1
	if view.Cfg.Count >= needed {
		return false, nil
	}

	if err := s.applyChange(ctx, view.Cfg.System, fieldCount, strconv.Itoa(needed)); err != nil {
		return false, fmt.Errorf("failed to bump count for system %s to %d: %w", systemName, needed, err)
	}
	logrus.WithFields(logrus.Fields{
		"system":    systemName,
		"namespace": namespace,
		"old_count": view.Cfg.Count,
		"new_count": needed,
	}).Infof("bumped chaos-system count to register namespace")
	return true, nil
}

// DeleteSystem marks the system as disabled/deleted by setting its etcd
// `status` key to CommonDeleted. The consumer watcher sees the transition
// and unregisters the system from chaos-experiment. Builtin systems cannot
// be deleted.
func (s *Service) DeleteSystem(ctx context.Context, id int) error {
	view, err := s.lookupByID(id)
	if err != nil {
		return err
	}
	if view.Cfg.IsBuiltin {
		return fmt.Errorf("cannot delete builtin system %s: %w", view.Cfg.System, consts.ErrBadRequest)
	}

	if err := s.applyChange(ctx, view.Cfg.System, fieldStatus, strconv.Itoa(int(consts.CommonDeleted))); err != nil {
		return err
	}

	// Best-effort local unregister. The watcher is the primary driver, but
	// we nudge the registry to stay consistent with existing behaviour for
	// callers that mutate state inside the same process.
	if chaos.IsSystemRegistered(view.Cfg.System) {
		if err := chaos.UnregisterSystem(view.Cfg.System); err != nil {
			logrus.WithError(err).Warnf("Failed to unregister system %s", view.Cfg.System)
		}
	}
	common.InvalidateGlobalMetadataStoreCache()
	return nil
}

func (s *Service) UpsertMetadata(_ context.Context, id int, req *BulkUpsertSystemMetadataReq) error {
	view, err := s.lookupByID(id)
	if err != nil {
		return err
	}

	for _, item := range req.Items {
		meta := &model.SystemMetadata{
			SystemName:   view.Cfg.System,
			MetadataType: common.NormalizeMetadataTypeForWrite(item.MetadataType),
			ServiceName:  item.ServiceName,
			Data:         string(item.Data),
		}
		if err := s.repo.UpsertSystemMetadata(meta); err != nil {
			return fmt.Errorf("failed to upsert metadata (type=%s, service=%s): %w", item.MetadataType, item.ServiceName, err)
		}
	}
	for _, svc := range req.Services {
		payload := common.ServiceTopologyData{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Pods:      append([]string(nil), svc.Pods...),
			DependsOn: append([]string(nil), svc.DependsOn...),
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal topology metadata for service %s: %w", svc.Name, err)
		}
		meta := &model.SystemMetadata{
			SystemName:   view.Cfg.System,
			MetadataType: common.NormalizeMetadataTypeForWrite("service_topology"),
			ServiceName:  svc.Name,
			Data:         string(raw),
		}
		if err := s.repo.UpsertSystemMetadata(meta); err != nil {
			return fmt.Errorf("failed to upsert topology metadata for service %s: %w", svc.Name, err)
		}
	}
	common.InvalidateGlobalMetadataStoreCache()
	return nil
}

func (s *Service) ListMetadata(_ context.Context, id int, metadataType string) ([]SystemMetadataResp, error) {
	view, err := s.lookupByID(id)
	if err != nil {
		return nil, err
	}
	metas, err := s.repo.ListSystemMetadata(view.Cfg.System, metadataType)
	if err != nil {
		return nil, fmt.Errorf("failed to list system metadata: %w", err)
	}
	items := make([]SystemMetadataResp, 0, len(metas))
	for _, meta := range metas {
		items = append(items, *NewSystemMetadataResp(&meta))
	}
	return items, nil
}

// Reseed drives initialization.ReseedFromDataFile against the live DB and
// etcd gateway. It is the HTTP-facing entry point for issue #105 — callers
// (aegisctl system reseed) use this to propagate data.yaml bumps (chart
// version / chart name / new container_version rows / dynamic_config
// default drift) to a running DB + etcd without redeploying the backend.
//
// The data file location is resolved from the `initialization.data_path`
// config key (the same key used by the first-boot seed path) so reseed and
// seed always read the same file. An optional `env` field lets operators
// pick prod/staging when the configured path is the initial_data root.
func (s *Service) ReseedSystems(ctx context.Context, req *ReseedSystemReq) (*initialization.ReseedReport, error) {
	if req == nil {
		req = &ReseedSystemReq{}
	}
	if !req.Apply {
		// Safety: default to dry-run so an accidental POST never writes.
		req.DryRun = true
	}
	basePath := strings.TrimSpace(req.DataPath)
	if basePath == "" {
		basePath = config.GetString("initialization.data_path")
	}
	seedPath, err := initialization.ResolveSeedPath(basePath, req.Env)
	if err != nil {
		return nil, fmt.Errorf("resolve seed path: %w: %w", err, consts.ErrBadRequest)
	}
	return initialization.ReseedFromDataFile(ctx, s.repo.DB(), s.etcd, initialization.ReseedRequest{
		DataPath:       seedPath,
		Env:            req.Env,
		SystemName:     strings.TrimSpace(req.Name),
		DryRun:         req.DryRun,
		ResetOverrides: req.ResetOverrides,
	})
}

// ListPrerequisites returns the declared cluster-level prereqs for a system
// (issue #115). Existence of the system is checked first so the caller gets
// 404 for an unknown name rather than an empty list masquerading as success.
func (s *Service) ListPrerequisites(_ context.Context, name string) ([]SystemPrerequisiteResp, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("system name is required: %w", consts.ErrBadRequest)
	}
	if _, err := s.lookupByName(name); err != nil {
		return nil, err
	}
	rows, err := s.repo.ListPrerequisites(name)
	if err != nil {
		return nil, err
	}
	out := make([]SystemPrerequisiteResp, 0, len(rows))
	for i := range rows {
		out = append(out, *NewSystemPrerequisiteResp(&rows[i]))
	}
	return out, nil
}

// ListInjectCandidates returns every reachable (app, chaos_type, target)
// tuple for the given system+namespace, with one entry per leaf in the
// guided enumeration tree (issue #181). Replaces the previous N-round-trip
// walk through `aegisctl inject guided` for adversarial / coverage-driven
// loops that need the full candidate pool up front.
//
// Existence of the system is checked before the enumeration so callers get
// a clean 404 for an unknown short code instead of an opaque "system X is
// not registered" error coming from chaos-experiment.
func (s *Service) ListInjectCandidates(ctx context.Context, name, namespace string) (*InjectCandidatesResp, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("system name is required: %w", consts.ErrBadRequest)
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil, fmt.Errorf("namespace is required: %w", consts.ErrBadRequest)
	}
	if _, err := s.lookupByName(name); err != nil {
		return nil, err
	}

	configs, err := enumerateCandidatesFn(ctx, name, namespace)
	if err != nil {
		// k8s/resourcelookup failures bubble up as 500. The CLI surfaces
		// the wrapped error message so callers see "list pods in
		// sockshop1: ..." rather than a bare "internal error".
		return nil, fmt.Errorf("enumerate candidates for %s/%s: %w: %w", name, namespace, err, consts.ErrInternal)
	}

	out := make([]InjectCandidateResp, 0, len(configs))
	for _, c := range configs {
		out = append(out, InjectCandidateResp{
			System:        c.System,
			SystemType:    c.SystemType,
			Namespace:     c.Namespace,
			App:           c.App,
			ChaosType:     c.ChaosType,
			Container:     c.Container,
			TargetService: c.TargetService,
			Domain:        c.Domain,
			Class:         c.Class,
			Method:        c.Method,
			MutatorConfig: c.MutatorConfig,
			Route:         c.Route,
			HTTPMethod:    c.HTTPMethod,
			Database:      c.Database,
			Table:         c.Table,
			Operation:     c.Operation,
		})
	}
	return &InjectCandidatesResp{Count: len(out), Candidates: out}, nil
}

// MarkPrerequisite updates the status of one prerequisite. aegisctl is the
// sole writer; backend never shells out to helm. "pending"/"failed"/"reconciled"
// are the only allowed values (validated by MarkPrerequisiteReq binding).
func (s *Service) MarkPrerequisite(_ context.Context, name string, id int, req *MarkPrerequisiteReq) (*SystemPrerequisiteResp, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("system name is required: %w", consts.ErrBadRequest)
	}
	row, err := s.repo.GetPrerequisiteByID(name, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("prerequisite %d for system %s: %w", id, name, consts.ErrNotFound)
	}
	if err := s.repo.UpdatePrerequisiteStatus(id, req.Status); err != nil {
		return nil, err
	}
	row.Status = req.Status
	return NewSystemPrerequisiteResp(row), nil
}

// =====================================================================
// Internal helpers
// =====================================================================

// lookupByID resolves the system whose anchor dynamic_config row has the
// given ID. We key lookups by the anchor row because it gives us a stable
// synthetic "system ID" without a dedicated table.
func (s *Service) lookupByID(id int) (*systemView, error) {
	configs, err := s.repo.ListSystemConfigs()
	if err != nil {
		return nil, err
	}
	anchors := buildAnchorIndex(configs)
	for _, anchor := range anchors {
		if anchor.ID != id {
			continue
		}
		name := systemNameFromKey(anchor.Key)
		cfgMap := config.GetChaosSystemConfigManager().GetAll()
		cfg, ok := cfgMap[name]
		if !ok {
			return nil, fmt.Errorf("system %s not found in etcd: %w", name, consts.ErrNotFound)
		}
		if !isSystemVisible(cfg) {
			return nil, fmt.Errorf("system %s has been deleted: %w", name, consts.ErrNotFound)
		}
		return newSystemView(anchor, cfg), nil
	}
	return nil, fmt.Errorf("system with id %d: %w", id, consts.ErrNotFound)
}

func (s *Service) lookupByName(name string) (*systemView, error) {
	anchor, err := s.repo.GetConfigByKey(systemKey(name, fieldCount))
	if err != nil {
		return nil, err
	}
	if anchor == nil {
		return nil, fmt.Errorf("system %s: %w", name, consts.ErrNotFound)
	}
	cfg, ok := config.GetChaosSystemConfigManager().Get(chaos.SystemType(name))
	if !ok {
		return nil, fmt.Errorf("system %s not loaded in viper", name)
	}
	return newSystemView(anchor, cfg), nil
}

// applyChange writes a new value for one field to etcd and records history.
func (s *Service) applyChange(ctx context.Context, system string, field systemField, newValue string) error {
	key := systemKey(system, field)
	anchor, err := s.repo.GetConfigByKey(key)
	if err != nil {
		return err
	}
	if anchor == nil {
		return fmt.Errorf("config %s not seeded: %w", key, consts.ErrNotFound)
	}

	etcdKey := consts.ConfigEtcdGlobalPrefix + key
	oldValue, err := s.etcd.Get(ctx, etcdKey)
	if err != nil {
		return fmt.Errorf("failed to get current value from etcd: %w", err)
	}

	if err := common.ValidateConfig(anchor, newValue); err != nil {
		return fmt.Errorf("invalid %s: %w", field, err)
	}

	if err := s.publishKey(ctx, key, newValue); err != nil {
		return err
	}
	if err := config.SetViperValue(key, newValue, anchor.ValueType); err != nil {
		return fmt.Errorf("failed to update local viper cache for %s: %w", key, err)
	}

	_ = s.repo.WriteHistory(&model.ConfigHistory{
		ChangeType:  consts.ChangeTypeUpdate,
		ChangeField: consts.ChangeFieldValue,
		OldValue:    oldValue,
		NewValue:    newValue,
		ConfigID:    anchor.ID,
	})
	return nil
}

// publishKey pushes value to etcd with a short retry. Global-scope only —
// injection.system.* keys are shared across producer + consumer. Retries
// honor ctx cancellation so a caller that gives up isn't held hostage by
// the backoff loop.
func (s *Service) publishKey(ctx context.Context, key, value string) error {
	etcdKey := consts.ConfigEtcdGlobalPrefix + key
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt > 0 {
			backoff := time.Duration(attempt) * 500 * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		ctxPut, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.etcd.Put(ctxPut, etcdKey, value, 0)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("failed to publish %s to etcd: %w", key, lastErr)
}

// saveAnchor updates a DynamicConfig row in place (for description edits).
func (s *Service) saveAnchor(cfg *model.DynamicConfig) error {
	return s.repo.SaveConfig(cfg)
}

// buildAnchorIndex maps each injection.system.<name>.count row to its model,
// keyed by full config_key so the caller can look up the anchor for a given
// system cheaply.
func buildAnchorIndex(configs []model.DynamicConfig) map[string]*model.DynamicConfig {
	out := make(map[string]*model.DynamicConfig)
	for i := range configs {
		cfg := configs[i]
		// Only the "count" field is used as the anchor.
		if strings.HasSuffix(cfg.Key, "."+string(fieldCount)) {
			out[cfg.Key] = &cfg
		}
	}
	return out
}

// systemNameFromKey strips the `injection.system.` prefix and the field
// suffix, returning the system name. Returns "" for malformed keys.
func systemNameFromKey(key string) string {
	const prefix = "injection.system."
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	rest := key[len(prefix):]
	idx := strings.LastIndex(rest, ".")
	if idx < 0 {
		return ""
	}
	return rest[:idx]
}

// valueTypeForField returns the ConfigValueType appropriate for each field.
func valueTypeForField(field systemField) consts.ConfigValueType {
	switch field {
	case fieldCount, fieldStatus:
		return consts.ConfigValueTypeInt
	case fieldIsBuiltin:
		return consts.ConfigValueTypeBool
	default:
		return consts.ConfigValueTypeString
	}
}

// isSystemVisible returns false for tombstoned systems. We deliberately do
// NOT filter disabled systems — they stay visible in list/get responses so
// admins can re-enable them through the same API.
func isSystemVisible(cfg config.ChaosSystemConfig) bool {
	return cfg.Status != consts.CommonDeleted
}
