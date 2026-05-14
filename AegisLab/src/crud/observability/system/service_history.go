package system

import (
	"context"
	"errors"
	"fmt"

	"aegis/core/orchestrator/common"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"
	"aegis/platform/utils"

	"gorm.io/gorm"
)

type configUpdateContext struct {
	ChangeField consts.ConfigHistoryChangeField
	OldValue    string
	NewValue    string
	Reason      string
	OperatorID  int
	IpAddress   string
	UserAgent   string
}

type configHistoryParams struct {
	ConfigID       int
	ChangeType     consts.ConfigHistoryChangeType
	RollbackFromID *int

	ConfigUpdateContext configUpdateContext
}

type configHistoryWriter interface {
	createConfigHistory(history *model.ConfigHistory) error
}

func (s *Service) ListConfigHistories(ctx context.Context, req *ListConfigHistoryReq, configID int) (*dto.ListResp[ConfigHistoryResp], error) {
	limit, offset := req.ToGormParams()

	histories, total, err := s.repo.listConfigHistories(limit, offset, configID, req.ChangeType, req.OperatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to list config histories: %w", err)
	}

	ids := make([]int, 0, len(histories))
	for i := range histories {
		if histories[i].OperatorID != nil {
			ids = append(ids, *histories[i].OperatorID)
		}
	}
	users := s.lookupUsers(ctx, ids)
	return buildConfigHistoryListResp(histories, req, total, users), nil
}

func (s *Service) RollbackConfigValue(ctx context.Context, req *RollbackConfigReq, configID, userID int, ipAddress, userAgent string) error {
	history, err := s.repo.getConfigHistory(req.HistoryID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: history entry with id %d not found", consts.ErrNotFound, req.HistoryID)
		}
		return fmt.Errorf("failed to get config history: %w", err)
	}

	if history.ChangeField != consts.ChangeFieldValue {
		return fmt.Errorf("history entry %d is not a value change (field: %v)", req.HistoryID, history.ChangeField)
	}

	existingConfig, err := s.repo.getConfigByID(configID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: configuration with id %d not found", consts.ErrNotFound, configID)
		}
		return fmt.Errorf("failed to get config: %w", err)
	}

	oldValue, err := s.etcd.Get(ctx, fmt.Sprintf("%s%s", etcdPrefixForScope(existingConfig.Scope), existingConfig.Key))
	if err != nil {
		return fmt.Errorf("failed to get current config value from etcd: %w", err)
	}

	newValue := history.OldValue
	if err := common.ValidateConfig(existingConfig, newValue); err != nil {
		return fmt.Errorf("invalid config after rollback: %w", err)
	}

	if err := setViperIfNeeded(existingConfig, newValue); err != nil {
		return fmt.Errorf("failed to set config value in viper: %w", err)
	}

	if _, err := s.createConfigRollback(existingConfig, utils.IntPtr(history.ID), configUpdateContext{
		ChangeField: consts.ChangeFieldValue,
		OldValue:    oldValue,
		NewValue:    newValue,
		Reason:      req.Reason,
		OperatorID:  userID,
		IpAddress:   ipAddress,
		UserAgent:   userAgent,
	}); err != nil {
		return err
	}

	return s.propagateValueChange(ctx, existingConfig, newValue, "rollback")
}

func (s *Service) RollbackConfigMetadata(ctx context.Context, req *RollbackConfigReq, configID, userID int, ipAddress, userAgent string) (*ConfigResp, error) {
	history, err := s.repo.getConfigHistory(req.HistoryID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: history entry with id %d not found", consts.ErrNotFound, req.HistoryID)
		}
		return nil, fmt.Errorf("failed to get config history: %w", err)
	}

	if history.ChangeField == consts.ChangeFieldValue {
		return nil, fmt.Errorf("history entry %d is a value change, use RollbackConfigValue instead", req.HistoryID)
	}

	existingConfig, err := s.repo.getConfigByID(configID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: configuration with id %d not found", consts.ErrNotFound, configID)
		}
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	oldValue, newValue, err := rollbackMetaFieldValue(existingConfig, history.ChangeField, history.OldValue)
	if err != nil {
		return nil, fmt.Errorf("failed to rollback metadata field: %w", err)
	}

	if err := common.ValidateConfigMetadataConstraints(existingConfig); err != nil {
		return nil, fmt.Errorf("invalid config after metadata rollback: %w", err)
	}

	updatedConfig, err := s.createConfigRollback(existingConfig, utils.IntPtr(history.ID), configUpdateContext{
		ChangeField: history.ChangeField,
		OldValue:    oldValue,
		NewValue:    newValue,
		Reason:      req.Reason,
		OperatorID:  userID,
		IpAddress:   ipAddress,
		UserAgent:   userAgent,
	})
	if err != nil {
		return nil, err
	}

	users := s.lookupUsers(ctx, configUserIDs(updatedConfig))
	return NewConfigResp(updatedConfig, users), nil
}

func (s *Service) createConfigHistory(repo configHistoryWriter, params configHistoryParams) error {
	entry := &model.ConfigHistory{
		ChangeType:       params.ChangeType,
		OldValue:         params.ConfigUpdateContext.OldValue,
		NewValue:         params.ConfigUpdateContext.NewValue,
		Reason:           params.ConfigUpdateContext.Reason,
		ConfigID:         params.ConfigID,
		OperatorID:       utils.IntPtr(params.ConfigUpdateContext.OperatorID),
		IPAddress:        params.ConfigUpdateContext.IpAddress,
		UserAgent:        params.ConfigUpdateContext.UserAgent,
		RolledBackFromID: params.RollbackFromID,
		ChangeField:      params.ConfigUpdateContext.ChangeField,
	}
	if err := repo.createConfigHistory(entry); err != nil {
		return fmt.Errorf("failed to create config history: %w", err)
	}
	return nil
}

func (s *Service) createConfigRollback(cfg *model.DynamicConfig, historyID *int, updateContext configUpdateContext) (*model.DynamicConfig, error) {
	var updatedConfig *model.DynamicConfig

	err := s.repo.db.Transaction(func(tx *gorm.DB) error {
		txRepo := NewRepository(tx)
		if err := txRepo.updateConfig(cfg); err != nil {
			return fmt.Errorf("failed to update config: %w", err)
		}

		updatedConfig = cfg
		if err := s.createConfigHistory(txRepo, configHistoryParams{
			ConfigID:            cfg.ID,
			ChangeType:          consts.ChangeTypeRollback,
			ConfigUpdateContext: updateContext,
			RollbackFromID:      historyID,
		}); err != nil {
			return fmt.Errorf("failed to create rollback history: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return updatedConfig, nil
}

func rollbackMetaFieldValue(cfg *model.DynamicConfig, changeField consts.ConfigHistoryChangeField, targetValue string) (string, string, error) {
	newValue := targetValue
	oldValue := ""

	switch changeField {
	case consts.ChangeFieldDefaultValue:
		oldValue = cfg.DefaultValue
		cfg.DefaultValue = newValue
	case consts.ChangeFieldDescription:
		oldValue = cfg.Description
		cfg.Description = newValue
	case consts.ChangeFieldMinValue:
		if cfg.MinValue != nil {
			oldValue = fmt.Sprintf("%f", *cfg.MinValue)
		}
		if newValue == "" {
			cfg.MinValue = nil
		} else {
			var minVal float64
			if _, err := fmt.Sscanf(newValue, "%f", &minVal); err != nil {
				return "", "", fmt.Errorf("failed to parse min value: %w", err)
			}
			cfg.MinValue = &minVal
		}
	case consts.ChangeFieldMaxValue:
		if cfg.MaxValue != nil {
			oldValue = fmt.Sprintf("%f", *cfg.MaxValue)
		}
		if newValue == "" {
			cfg.MaxValue = nil
		} else {
			var maxVal float64
			if _, err := fmt.Sscanf(newValue, "%f", &maxVal); err != nil {
				return "", "", fmt.Errorf("failed to parse max value: %w", err)
			}
			cfg.MaxValue = &maxVal
		}
	case consts.ChangeFieldPattern:
		oldValue = cfg.Pattern
		cfg.Pattern = newValue
	case consts.ChangeFieldOptions:
		oldValue = cfg.Options
		cfg.Options = newValue
	default:
		return "", "", fmt.Errorf("unknown change field: %d", changeField)
	}

	return oldValue, newValue, nil
}
