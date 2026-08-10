package authz

import (
	"context"
	"fmt"
	"strconv"

	"aegis/platform/consts"
	"aegis/platform/model"

	"gorm.io/gorm"
)

// GormProjectMembershipResolver loads visible projects from user_scoped_roles
// (scope_type="aegis.project", status=enabled).
type GormProjectMembershipResolver struct {
	db *gorm.DB
}

func NewGormProjectMembershipResolver(db *gorm.DB) *GormProjectMembershipResolver {
	return &GormProjectMembershipResolver{db: db}
}

func (r *GormProjectMembershipResolver) VisibleProjects(ctx context.Context, userID int64) ([]int64, error) {
	var rows []model.UserScopedRole
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND scope_type = ? AND status = ?",
			userID, consts.ScopeTypeProject, consts.CommonEnabled).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("authz: load visible projects: %w", err)
	}

	out := make([]int64, 0, len(rows))
	seen := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		id, err := strconv.ParseInt(row.ScopeID, 10, 64)
		if err != nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}
