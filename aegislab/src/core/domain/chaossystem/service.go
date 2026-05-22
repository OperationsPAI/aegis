package chaossystem

import (
	"context"
	"encoding/json"
	"errors"
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
	containerapi "aegis/core/domain/container"
	"aegis/boot/seed"

	chaos "aegis/platform/chaos"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

// enumerateCandidatesFn is the indirection used by ListInjectCandidates so
// tests can inject a fixture without standing up a chaos-service double.
// Defaults to a thin HTTP proxy against the chaos-service
// /v1beta/systems/{sys}/candidates endpoint (PR phase 2e). The backend
// /api/v2/chaos-systems/{system}/candidates handler is now a pass-through.
var enumerateCandidatesFn = enumerateCandidatesViaChaosService

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
	Delete(ctx context.Context, key string) error
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
	// chaos-service registry on status/ns_pattern events.
	for _, field := range allSystemFields() {
		if err := s.publishKey(ctx, systemKey(name, field), defaults[field]); err != nil {
			return nil, fmt.Errorf("failed to publish %s to etcd: %w", field, err)
		}
	}

	// chaos-service owns its own metadata cache; cross-process invalidation
	// from aegislab is no longer the right thing to do — the watch handler
	// keeps Viper in sync and chaos-service refreshes on demand.

	// The config manager reads Viper on demand; the consumer watch handler
	// will keep Viper in sync when the etcd event round-trips back.
	view, err := s.lookupByName(name)
	if err != nil {
		return nil, err
	}
	return NewChaosSystemResp(view), nil
}

// OnboardSystem is the atomic composite of CreateSystem + CreateContainer +
// chart binding. The DB writes (container + container_version + helm_config
// + dynamic_config) commit in a single transaction; the 7 etcd identity
// keys are published only after the tx succeeds. On etcd failure mid-flight
// the already-published keys are best-effort deleted so a partial onboard
// never leaves a half-registered system that pedestal chart install would
// reject.
func (s *Service) OnboardSystem(ctx context.Context, req *OnboardSystemReq) (*OnboardSystemResp, error) {
	if req == nil {
		return nil, fmt.Errorf("request body required: %w", consts.ErrBadRequest)
	}

	sysReq := req.System
	sysReq.Name = strings.TrimSpace(sysReq.Name)
	if sysReq.Name == "" {
		return nil, fmt.Errorf("system.name is required: %w", consts.ErrBadRequest)
	}
	if _, err := regexp.Compile(sysReq.NsPattern); err != nil {
		return nil, fmt.Errorf("invalid system.ns_pattern regex: %w: %w", err, consts.ErrBadRequest)
	}
	if _, err := regexp.Compile(sysReq.ExtractPattern); err != nil {
		return nil, fmt.Errorf("invalid system.extract_pattern regex: %w: %w", err, consts.ErrBadRequest)
	}
	if err := req.Container.Validate(); err != nil {
		return nil, fmt.Errorf("invalid container payload: %w: %w", err, consts.ErrBadRequest)
	}

	// Conflict on system identity (the anchor dynamic_config row). Container
	// name conflicts surface from createContainerCore as ErrAlreadyExists.
	existing, err := s.repo.GetConfigByKey(systemKey(sysReq.Name, fieldCount))
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("system %s already exists: %w", sysReq.Name, consts.ErrAlreadyExists)
	}

	appLabelKey := normalizeAppLabelKey(sysReq.AppLabelKey)
	defaults := map[systemField]string{
		fieldCount:          strconv.Itoa(sysReq.Count),
		fieldNsPattern:      sysReq.NsPattern,
		fieldExtractPattern: sysReq.ExtractPattern,
		fieldDisplayName:    sysReq.DisplayName,
		fieldAppLabelKey:    appLabelKey,
		fieldIsBuiltin:      strconv.FormatBool(sysReq.IsBuiltin),
		fieldStatus:         strconv.Itoa(int(consts.CommonEnabled)),
	}
	descriptions := map[systemField]string{
		fieldCount:          fmt.Sprintf("Number of system %s to create", sysReq.DisplayName),
		fieldNsPattern:      fmt.Sprintf("Namespace pattern for system %s instances", sysReq.DisplayName),
		fieldExtractPattern: fmt.Sprintf("Extraction pattern for namespace prefix and number from %s instances", sysReq.DisplayName),
		fieldDisplayName:    fmt.Sprintf("Human-readable display name for system %s", sysReq.Name),
		fieldAppLabelKey:    fmt.Sprintf("Kubernetes pod label key used to select %s workloads", sysReq.DisplayName),
		fieldIsBuiltin:      fmt.Sprintf("Whether %s is a builtin benchmark system", sysReq.DisplayName),
		fieldStatus:         fmt.Sprintf("Status of system %s (1=enabled, 0=disabled, -1=deleted)", sysReq.DisplayName),
	}
	if sysReq.Description != "" {
		descriptions[fieldCount] = sysReq.Description
	}

	userID := userIDFromCtx(ctx)
	containerModel := req.Container.ConvertToContainer()
	configIDs := make([]int, 0, len(allSystemFields()))

	err = s.repo.DB().Transaction(func(tx *gorm.DB) error {
		txRepo := NewRepository(tx)
		for _, field := range allSystemFields() {
			cfg := &model.DynamicConfig{
				Key:          systemKey(sysReq.Name, field),
				DefaultValue: defaults[field],
				ValueType:    valueTypeForField(field),
				Scope:        consts.ConfigScopeGlobal,
				Category:     "injection.system." + string(field),
				Description:  descriptions[field],
			}
			if err := txRepo.CreateConfig(cfg); err != nil {
				// Two concurrent onboards for the same code both pass the
				// pre-tx existence probe; the loser hits the unique index on
				// dynamic_configs.config_key inside the tx. Map to 409 so
				// the handler does not return 500.
				if errors.Is(err, gorm.ErrDuplicatedKey) {
					return fmt.Errorf("system %s already exists: %w", sysReq.Name, consts.ErrAlreadyExists)
				}
				return err
			}
			configIDs = append(configIDs, cfg.ID)
		}
		if _, err := containerapi.NewRepository(tx).CreateContainerCore(containerModel, userID); err != nil {
			if errors.Is(err, consts.ErrAlreadyExists) {
				return fmt.Errorf("container %s already exists: %w", containerModel.Name, consts.ErrAlreadyExists)
			}
			return fmt.Errorf("create container: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Publish identity to etcd. On failure mid-flight we compensate by
	// deleting the already-published etcd keys AND the committed DB rows
	// (dynamic_configs hard-delete, container soft-delete). Without the DB
	// rollback the pre-tx existence probe at the top of OnboardSystem would
	// surface a 409 on a retry, leaving the orphan unrecoverable through the
	// supported API.
	published := make([]string, 0, len(allSystemFields()))
	for _, field := range allSystemFields() {
		key := systemKey(sysReq.Name, field)
		if err := s.publishKey(ctx, key, defaults[field]); err != nil {
			s.bestEffortDeletePublished(ctx, published)
			s.compensateOnboardDB(configIDs, containerModel.ID)
			return nil, fmt.Errorf("publish %s to etcd: %w", field, err)
		}
		published = append(published, key)
	}

	view, err := s.lookupByName(sysReq.Name)
	if err != nil {
		return nil, err
	}
	return &OnboardSystemResp{
		System:    *NewChaosSystemResp(view),
		Container: *containerapi.NewContainerResp(containerModel),
	}, nil
}

func (s *Service) bestEffortDeletePublished(ctx context.Context, keys []string) {
	for _, k := range keys {
		etcdKey := consts.ConfigEtcdGlobalPrefix + k
		if err := s.etcd.Delete(ctx, etcdKey); err != nil {
			logrus.WithFields(logrus.Fields{"key": etcdKey, "err": err}).
				Warn("onboard rollback: failed to delete etcd key")
		}
	}
}

// compensateOnboardDB reverses the committed DB writes when etcd publish
// fails after the tx commits. dynamic_configs has no soft-delete column so
// rows are hard-deleted by primary key; the container goes through the
// same status=CommonDeleted path as DeleteContainer so the active_name
// virtual column unblocks a re-onboard with the same code.
func (s *Service) compensateOnboardDB(configIDs []int, containerID int) {
	db := s.repo.DB()
	if len(configIDs) > 0 {
		if err := db.Where("id IN ?", configIDs).Delete(&model.DynamicConfig{}).Error; err != nil {
			logrus.WithFields(logrus.Fields{"ids": configIDs, "err": err}).
				Warn("onboard rollback: failed to delete dynamic_configs rows")
		}
	}
	if containerID > 0 {
		res := db.Model(&model.Container{}).
			Where("id = ? AND status != ?", containerID, consts.CommonDeleted).
			Update("status", consts.CommonDeleted)
		if res.Error != nil {
			logrus.WithFields(logrus.Fields{"id": containerID, "err": res.Error}).
				Warn("onboard rollback: failed to soft-delete container")
		}
	}
}

// ExportSeed returns a data.yaml-compatible YAML snippet for the given
// system, suitable for `>> data/initial_data/<env>/data.yaml`. The output
// embeds both halves — the 7 dynamic_configs rows and a matching
// containers entry (with the pedestal chart binding) — so a runtime
// onboard can be checked back into git for reproducible cold-start.
//
// Format must round-trip through `aegisctl system register --from-seed`
// for the system-identity half; the containers half is consumed by the
// boot seed loader at cluster bring-up.
func (s *Service) ExportSeed(_ context.Context, name string) (*ExportSeedResp, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("system name is required: %w", consts.ErrBadRequest)
	}
	view, err := s.lookupByName(name)
	if err != nil {
		return nil, err
	}

	configs, err := s.repo.ListSystemConfigs()
	if err != nil {
		return nil, err
	}
	prefix := systemKeyPrefix(name)
	rows := make([]seedDynamicConfigYAML, 0, len(allSystemFields()))
	for _, cfg := range configs {
		if !strings.HasPrefix(cfg.Key, prefix) {
			continue
		}
		field := strings.TrimPrefix(cfg.Key, prefix)
		if _, ok := expectedExportedFields[field]; !ok {
			continue
		}
		rows = append(rows, seedDynamicConfigYAML{
			Key:          cfg.Key,
			DefaultValue: cfg.DefaultValue,
			ValueType:    int(cfg.ValueType),
			Scope:        int(cfg.Scope),
			Category:     cfg.Category,
			Description:  cfg.Description,
			IsSecret:     false,
		})
	}

	// Containers half: prefer to emit the pedestal chart binding when one is
	// bound to this system. Soft-skip when the system has no pedestal —
	// export-seed still produces the dynamic_configs half so identity-only
	// systems round-trip cleanly.
	containers := []seedContainerYAML{}
	helm, version, err := s.repo.GetPedestalHelmConfigByName(view.Cfg.System, "")
	if err == nil && helm != nil && version != nil {
		containers = append(containers, seedContainerYAML{
			Type:     int(consts.ContainerTypePedestal),
			Name:     view.Cfg.System,
			IsPublic: true,
			Status:   int(consts.CommonEnabled),
			Versions: []seedContainerVersionYAML{{
				Name:       version.Name,
				GithubLink: version.GithubLink,
				Status:     int(consts.CommonEnabled),
				HelmConfig: &seedHelmConfigYAML{
					Version:   helm.Version,
					ChartName: helm.ChartName,
					RepoName:  helm.RepoName,
					RepoURL:   helm.RepoURL,
					Status:    int(consts.CommonEnabled),
				},
			}},
		})
	}

	doc := seedExportDoc{
		Containers:     containers,
		DynamicConfigs: rows,
	}
	buf, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("encode seed yaml: %w", err)
	}
	return &ExportSeedResp{YAML: string(buf)}, nil
}

// expectedExportedFields mirrors aegisctl's `system register --from-seed`
// schema: the 7 identity fields. Other prefixed rows are skipped so the
// snippet is byte-identical to a hand-curated data.yaml block.
var expectedExportedFields = map[string]struct{}{
	string(fieldCount):          {},
	string(fieldNsPattern):      {},
	string(fieldExtractPattern): {},
	string(fieldDisplayName):    {},
	string(fieldAppLabelKey):    {},
	string(fieldIsBuiltin):      {},
	string(fieldStatus):         {},
}

type seedExportDoc struct {
	Containers     []seedContainerYAML     `yaml:"containers,omitempty"`
	DynamicConfigs []seedDynamicConfigYAML `yaml:"dynamic_configs"`
}

type seedDynamicConfigYAML struct {
	Key          string `yaml:"key"`
	DefaultValue string `yaml:"default_value"`
	ValueType    int    `yaml:"value_type"`
	Scope        int    `yaml:"scope"`
	Category     string `yaml:"category"`
	Description  string `yaml:"description"`
	IsSecret     bool   `yaml:"is_secret"`
}

type seedContainerYAML struct {
	Type     int                        `yaml:"type"`
	Name     string                     `yaml:"name"`
	IsPublic bool                       `yaml:"is_public"`
	Status   int                        `yaml:"status"`
	Versions []seedContainerVersionYAML `yaml:"versions"`
}

type seedContainerVersionYAML struct {
	Name       string              `yaml:"name"`
	GithubLink string              `yaml:"github_link,omitempty"`
	Status     int                 `yaml:"status"`
	HelmConfig *seedHelmConfigYAML `yaml:"helm_config,omitempty"`
}

type seedHelmConfigYAML struct {
	Version   string `yaml:"version"`
	ChartName string `yaml:"chart_name"`
	RepoName  string `yaml:"repo_name"`
	RepoURL   string `yaml:"repo_url"`
	Status    int    `yaml:"status"`
}

// userIDFromCtx pulls the authenticated user ID for ownership stamping on
// the container row. Falls back to 0 (system) when the request is from a
// non-user context (e.g. boot-time tests).
func userIDFromCtx(ctx context.Context) int {
	if v := ctx.Value(consts.CtxKeyUserID); v != nil {
		if id, ok := v.(int); ok {
			return id
		}
	}
	return 0
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
// and unregisters the system from the chaos-service registry. Builtin
// systems cannot be deleted.
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

	// Local registry side-effects are gone — chaos-service owns the registry
	// and reconciles on the etcd status flip.
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
// not registered" error from the chaos-service registry.
func (s *Service) ListInjectCandidates(ctx context.Context, name, namespace string) (*InjectCandidatesResp, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("system name is required: %w", consts.ErrBadRequest)
	}
	namespace = strings.TrimSpace(namespace)
	view, err := s.lookupByName(name)
	if err != nil {
		return nil, err
	}

	// InjectionCreate's auto-namespace mode hits this endpoint before a
	// concrete pool namespace has been allocated, so the request comes in
	// with namespace="". Fan out across every namespace in the system's
	// pool and union the results (dedup by candidate identity) so the
	// wizard's app dropdown has a real list to show. The frontend already
	// strips the per-namespace `Namespace` field from each candidate before
	// rendering, so it's safe to return entries from arbitrary pool slots
	// here.
	namespaces := []string{namespace}
	if namespace == "" {
		namespaces = view.Cfg.Namespaces()
		if len(namespaces) == 0 {
			// No enumerable pool — return empty rather than fabricating.
			return &InjectCandidatesResp{Count: 0, Candidates: []InjectCandidateResp{}}, nil
		}
	}

	seen := make(map[string]struct{})
	out := make([]InjectCandidateResp, 0)
	for _, ns := range namespaces {
		configs, enumErr := enumerateCandidatesFn(ctx, name, ns)
		if enumErr != nil {
			// k8s/resourcelookup failures bubble up as 500. The CLI
			// surfaces the wrapped error message so callers see "list
			// pods in sockshop1: ..." rather than a bare "internal
			// error". For the auto-namespace fan-out we still abort on
			// the first failure: a partial union would silently hide
			// misconfiguration in one pool slot, which is worse than a
			// loud 500.
			return nil, fmt.Errorf("enumerate candidates for %s/%s: %w: %w", name, ns, enumErr, consts.ErrInternal)
		}
		for _, c := range configs {
			cand := InjectCandidateResp{
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
			}
			// In auto-namespace mode the same (app, chaos_type, target)
			// tuple appears once per pool slot — dedup on the identity
			// fields (Namespace deliberately excluded) so the wizard
			// sees one entry per logical candidate.
			key := candidateIdentityKey(cand, namespace == "")
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, cand)
		}
	}
	return &InjectCandidatesResp{Count: len(out), Candidates: out}, nil
}

// candidateIdentityKey produces a deterministic dedup key for one inject
// candidate. When namespaceWildcard is true the Namespace field is dropped so
// equivalent candidates across pool slots collapse to one row.
func candidateIdentityKey(c InjectCandidateResp, namespaceWildcard bool) string {
	parts := []string{
		c.System, c.SystemType,
		c.App, c.ChaosType, c.Container, c.TargetService,
		c.Domain, c.Class, c.Method, c.MutatorConfig,
		c.Route, c.HTTPMethod,
		c.Database, c.Table, c.Operation,
	}
	if !namespaceWildcard {
		parts = append(parts, c.Namespace)
	}
	return strings.Join(parts, "\x1f")
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
