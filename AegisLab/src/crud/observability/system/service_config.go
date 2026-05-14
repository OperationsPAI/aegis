package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"aegis/core/orchestrator/common"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"
	"aegis/platform/utils"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func (s *Service) GetConfig(ctx context.Context, configID int) (*ConfigDetailResp, error) {
	cfg, err := s.repo.getConfigByID(configID)
	if err != nil {
		return nil, fmt.Errorf("failed to get config detail: %w", err)
	}

	histories, err := s.repo.listConfigHistoriesByConfigID(cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get config histories: %w", err)
	}

	ids := collectConfigUserIDs(cfg, histories)
	users := s.lookupUsers(ctx, ids)
	return buildConfigDetailResp(cfg, histories, users), nil
}

func (s *Service) ListConfigs(ctx context.Context, req *ListConfigReq) (*dto.ListResp[ConfigResp], error) {
	limit, offset := req.ToGormParams()

	configs, total, err := s.repo.listConfigs(limit, offset, req.ValueType, req.Category, req.IsSecret, req.UpdatedBy)
	if err != nil {
		return nil, fmt.Errorf("failed to list configs: %w", err)
	}

	ids := make([]int, 0, len(configs))
	for i := range configs {
		if configs[i].UpdatedBy != nil {
			ids = append(ids, *configs[i].UpdatedBy)
		}
	}
	users := s.lookupUsers(ctx, ids)
	return buildConfigListResp(configs, req, total, users), nil
}

func (s *Service) UpdateConfigValue(ctx context.Context, req *UpdateConfigValueReq, configID, userID int, ipAddress, userAgent string) error {
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

	newValue := req.Value
	if err := common.ValidateConfig(existingConfig, newValue); err != nil {
		return fmt.Errorf("invalid config value: %w", err)
	}

	if err := setViperIfNeeded(existingConfig, newValue); err != nil {
		return fmt.Errorf("failed to set config value in viper: %w", err)
	}

	if err := s.createConfigHistory(s.repo, configHistoryParams{
		ConfigID:   existingConfig.ID,
		ChangeType: consts.ChangeTypeUpdate,
		ConfigUpdateContext: configUpdateContext{
			ChangeField: consts.ChangeFieldValue,
			OldValue:    oldValue,
			NewValue:    newValue,
			Reason:      req.Reason,
			OperatorID:  userID,
			IpAddress:   ipAddress,
			UserAgent:   userAgent,
		},
	}); err != nil {
		return fmt.Errorf("failed to create config history: %w", err)
	}

	return s.propagateValueChange(ctx, existingConfig, newValue, "update")
}

func (s *Service) UpdateConfigMetadata(ctx context.Context, req *UpdateConfigMetadataReq, configID, userID int, ipAddress, userAgent string) (*ConfigResp, error) {
	existingConfig, err := s.repo.getConfigByID(configID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: configuration with id %d not found", consts.ErrNotFound, configID)
		}
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	oldValue, newValue := req.PatchConfigModel(existingConfig)
	if err := common.ValidateConfigMetadataConstraints(existingConfig); err != nil {
		return nil, fmt.Errorf("invalid config after metadata update: %w", err)
	}

	var updatedConfig *model.DynamicConfig
	err = s.repo.db.Transaction(func(tx *gorm.DB) error {
		txRepo := NewRepository(tx)
		existingConfig.UpdatedBy = utils.IntPtr(userID)

		if err := txRepo.updateConfig(existingConfig); err != nil {
			return fmt.Errorf("failed to update config: %w", err)
		}

		updatedConfig = existingConfig
		if err := s.createConfigHistory(txRepo, configHistoryParams{
			ConfigID:   updatedConfig.ID,
			ChangeType: consts.ChangeTypeUpdate,
			ConfigUpdateContext: configUpdateContext{
				ChangeField: req.GetChangeField(),
				OldValue:    oldValue,
				NewValue:    newValue,
				Reason:      req.Reason,
				OperatorID:  userID,
				IpAddress:   ipAddress,
				UserAgent:   userAgent,
			},
		}); err != nil {
			return fmt.Errorf("failed to create config history: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	users := s.lookupUsers(ctx, configUserIDs(updatedConfig))
	return NewConfigResp(updatedConfig, users), nil
}

func (s *Service) propagateValueChange(ctx context.Context, cfg *model.DynamicConfig, newValue, opDesc string) error {
	if cfg.Scope != consts.ConfigScopeGlobal && cfg.Scope != consts.ConfigScopeConsumer {
		return nil
	}

	etcdKey := fmt.Sprintf("%s%s", etcdPrefixForScope(cfg.Scope), cfg.Key)
	if err := s.publishConfigToEtcdWithRetry(ctx, etcdKey, newValue, 3); err != nil {
		return fmt.Errorf("config saved to database but failed to publish to etcd: %w", err)
	}

	if cfg.Scope == consts.ConfigScopeConsumer {
		logrus.Infof("Waiting for consumer config %s response...", opDesc)
		resp, err := s.waitForConfigUpdateResponse(ctx, 10*time.Second)
		if err != nil {
			return fmt.Errorf("config %s but consumer did not respond: %w", opDesc, err)
		}
		if !resp.Success {
			return fmt.Errorf("consumer failed to process config %s: %s", opDesc, resp.Error)
		}
		logrus.Infof("Config %s successfully processed by consumer", opDesc)
	}

	return nil
}

func (s *Service) publishConfigToEtcdWithRetry(ctx context.Context, key, value string, maxRetries int) error {
	var lastErr error
	baseDelay := 500 * time.Millisecond

	for attempt := range maxRetries {
		if attempt > 0 {
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			logrus.Warnf("Retrying etcd publish after %v (attempt %d/%d)", delay, attempt+1, maxRetries)
			time.Sleep(delay)
		}

		publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.etcd.Put(publishCtx, key, value, 0)
		cancel()

		if err == nil {
			if attempt > 0 {
				logrus.Infof("Successfully published config to etcd after %d retries", attempt)
			}
			return nil
		}

		lastErr = err
		logrus.Warnf("Failed to publish config to etcd (attempt %d/%d): %v", attempt+1, maxRetries, err)
	}

	return fmt.Errorf("failed to publish config to etcd after %d attempts: %w", maxRetries, lastErr)
}

func (s *Service) waitForConfigUpdateResponse(parent context.Context, timeout time.Duration) (*dto.ConfigUpdateResponse, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	pubsub, err := s.redis.Subscribe(ctx, consts.ConfigUpdateResponseChannel)
	if err != nil {
		return nil, fmt.Errorf("failed to confirm subscription: %w", err)
	}
	defer func() { _ = pubsub.Close() }()

	msgChan := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for config update response after %v", timeout)
		case msg, ok := <-msgChan:
			if !ok {
				return nil, fmt.Errorf("subscription channel closed unexpectedly")
			}

			var response dto.ConfigUpdateResponse
			if err := json.Unmarshal([]byte(msg.Payload), &response); err != nil {
				logrus.Warnf("failed to parse response message: %v", err)
				continue
			}

			logrus.WithFields(logrus.Fields{
				"response_id": response.ID,
				"success":     response.Success,
			}).Info("Received matching config update response")
			return &response, nil
		}
	}
}
