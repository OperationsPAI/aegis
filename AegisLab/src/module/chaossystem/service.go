package chaossystem

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"aegis/config"
	"aegis/consts"
	"aegis/dto"
	"aegis/model"
	"aegis/service/common"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/sirupsen/logrus"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func normalizeAppLabelKey(key string) string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return "app"
	}
	return trimmed
}

func reloadSystemConfigs() {
	if err := config.GetChaosSystemConfigManager().Reload(func() error { return nil }); err != nil {
		logrus.WithError(err).Warn("Failed to reload chaos system config manager")
	}
}

func syncSystemRegistration(system *model.System) {
	if system == nil {
		return
	}
	if chaos.IsSystemRegistered(system.Name) {
		if err := chaos.UnregisterSystem(system.Name); err != nil {
			logrus.WithError(err).Warnf("Failed to unregister existing system %s", system.Name)
		}
	}
	if system.Status != consts.CommonEnabled {
		return
	}
	if err := chaos.RegisterSystem(chaos.SystemConfig{
		Name:        system.Name,
		NsPattern:   system.NsPattern,
		DisplayName: system.DisplayName,
		AppLabelKey: normalizeAppLabelKey(system.AppLabelKey),
	}); err != nil {
		logrus.WithError(err).Warnf("Failed to register system %s with chaos-experiment", system.Name)
	}
}

func (s *Service) ListSystems(_ context.Context, req *ListChaosSystemReq) (*dto.ListResp[ChaosSystemResp], error) {
	limit, offset := req.ToGormParams()
	systems, total, err := s.repo.ListSystems(limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list systems: %w", err)
	}

	items := make([]ChaosSystemResp, 0, len(systems))
	for _, item := range systems {
		items = append(items, *NewChaosSystemResp(&item))
	}

	return &dto.ListResp[ChaosSystemResp]{
		Items:      items,
		Pagination: req.ConvertToPaginationInfo(total),
	}, nil
}

func (s *Service) GetSystem(_ context.Context, id int) (*ChaosSystemResp, error) {
	system, err := s.repo.GetSystemByID(id)
	if err != nil {
		return nil, err
	}
	return NewChaosSystemResp(system), nil
}

func (s *Service) CreateSystem(_ context.Context, req *CreateChaosSystemReq) (*ChaosSystemResp, error) {
	if _, err := regexp.Compile(req.NsPattern); err != nil {
		return nil, fmt.Errorf("invalid ns_pattern regex: %w: %w", err, consts.ErrBadRequest)
	}
	if _, err := regexp.Compile(req.ExtractPattern); err != nil {
		return nil, fmt.Errorf("invalid extract_pattern regex: %w: %w", err, consts.ErrBadRequest)
	}

	system := &model.System{
		Name:           req.Name,
		DisplayName:    req.DisplayName,
		NsPattern:      req.NsPattern,
		ExtractPattern: req.ExtractPattern,
		AppLabelKey:    normalizeAppLabelKey(req.AppLabelKey),
		Count:          req.Count,
		Description:    req.Description,
		IsBuiltin:      false,
		Status:         consts.CommonEnabled,
	}

	if err := s.repo.CreateSystem(system); err != nil {
		return nil, fmt.Errorf("failed to create system: %w", err)
	}
	syncSystemRegistration(system)
	reloadSystemConfigs()
	common.InvalidateGlobalMetadataStoreCache()

	return NewChaosSystemResp(system), nil
}

func (s *Service) UpdateSystem(_ context.Context, id int, req *UpdateChaosSystemReq) (*ChaosSystemResp, error) {
	system, err := s.repo.GetSystemByID(id)
	if err != nil {
		return nil, err
	}

	updates := make(map[string]interface{})
	if req.DisplayName != nil {
		updates["display_name"] = *req.DisplayName
	}
	if req.NsPattern != nil {
		if _, err := regexp.Compile(*req.NsPattern); err != nil {
			return nil, fmt.Errorf("invalid ns_pattern regex: %w: %w", err, consts.ErrBadRequest)
		}
		updates["ns_pattern"] = *req.NsPattern
	}
	if req.ExtractPattern != nil {
		if _, err := regexp.Compile(*req.ExtractPattern); err != nil {
			return nil, fmt.Errorf("invalid extract_pattern regex: %w: %w", err, consts.ErrBadRequest)
		}
		updates["extract_pattern"] = *req.ExtractPattern
	}
	if req.AppLabelKey != nil {
		updates["app_label_key"] = normalizeAppLabelKey(*req.AppLabelKey)
	}
	if req.Count != nil {
		updates["count"] = *req.Count
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if len(updates) == 0 {
		return NewChaosSystemResp(system), nil
	}

	if err := s.repo.UpdateSystem(id, updates); err != nil {
		return nil, err
	}
	system, err = s.repo.GetSystemByID(id)
	if err != nil {
		return nil, err
	}
	syncSystemRegistration(system)
	reloadSystemConfigs()
	common.InvalidateGlobalMetadataStoreCache()

	return NewChaosSystemResp(system), nil
}

func (s *Service) DeleteSystem(_ context.Context, id int) error {
	system, err := s.repo.GetSystemByID(id)
	if err != nil {
		return err
	}
	if system.IsBuiltin {
		return fmt.Errorf("cannot delete builtin system %s: %w", system.Name, consts.ErrBadRequest)
	}
	if err := s.repo.DeleteSystem(id); err != nil {
		return err
	}
	if err := chaos.UnregisterSystem(system.Name); err != nil {
		logrus.WithError(err).Warnf("Failed to unregister system %s from chaos-experiment", system.Name)
	}
	reloadSystemConfigs()
	common.InvalidateGlobalMetadataStoreCache()
	return nil
}

func (s *Service) UpsertMetadata(_ context.Context, id int, req *BulkUpsertSystemMetadataReq) error {
	system, err := s.repo.GetSystemByID(id)
	if err != nil {
		return err
	}

	for _, item := range req.Items {
		meta := &model.SystemMetadata{
			SystemName:   system.Name,
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
			SystemName:   system.Name,
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
	system, err := s.repo.GetSystemByID(id)
	if err != nil {
		return nil, err
	}
	metas, err := s.repo.ListSystemMetadata(system.Name, metadataType)
	if err != nil {
		return nil, fmt.Errorf("failed to list system metadata: %w", err)
	}
	items := make([]SystemMetadataResp, 0, len(metas))
	for _, meta := range metas {
		items = append(items, *NewSystemMetadataResp(&meta))
	}
	return items, nil
}
