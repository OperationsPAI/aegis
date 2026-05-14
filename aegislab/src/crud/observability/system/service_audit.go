package system

import (
	"context"
	"errors"
	"fmt"

	"aegis/clients/sso"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// lookupUsers fetches user info from SSO for the given ids, swallowing errors
// because audit/config display is best-effort enrichment, not load-bearing.
func (s *Service) lookupUsers(ctx context.Context, ids []int) map[int]*ssoclient.UserInfo {
	if len(ids) == 0 || s.sso == nil {
		return nil
	}
	users, err := s.sso.GetUsers(ctx, ids)
	if err != nil {
		logrus.WithError(err).Warn("system: ssoclient.GetUsers failed; user names will be missing")
		return nil
	}
	return users
}

func (s *Service) GetAuditLog(ctx context.Context, id int) (*AuditLogDetailResp, error) {
	log, err := s.repo.getAuditLogByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: audit log with ID %d not found", consts.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get audit log: %w", err)
	}

	users := s.lookupUsers(ctx, []int{log.UserID})
	return NewAuditLogDetailResp(log, users), nil
}

func (s *Service) ListAuditLogs(ctx context.Context, req *ListAuditLogReq) (*dto.ListResp[AuditLogResp], error) {
	limit, offset := req.ToGormParams()
	filterOptions := req.ToFilterOptions()

	logs, total, err := s.repo.listAuditLogs(limit, offset, filterOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list audit logs: %w", err)
	}

	ids := make([]int, 0, len(logs))
	for i := range logs {
		ids = append(ids, logs[i].UserID)
	}
	users := s.lookupUsers(ctx, ids)
	return buildAuditLogListResp(logs, req, total, users), nil
}

func configUserIDs(cfg *model.DynamicConfig) []int {
	if cfg == nil || cfg.UpdatedBy == nil {
		return nil
	}
	return []int{*cfg.UpdatedBy}
}

func collectConfigUserIDs(cfg *model.DynamicConfig, histories []model.ConfigHistory) []int {
	ids := configUserIDs(cfg)
	for i := range histories {
		if histories[i].OperatorID != nil {
			ids = append(ids, *histories[i].OperatorID)
		}
	}
	return ids
}
