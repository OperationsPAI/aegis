package execution

import (
	"context"
	"fmt"

	"aegis/platform/authz"
	"aegis/platform/consts"
	"aegis/platform/model"

	"gorm.io/gorm"
)

// Cascader owns destructive writes to the executions table on behalf of
// other domains (currently injection). Holding these writes here keeps the
// scope check (project_id IN scope.VisibleProjects) co-located with the
// table that authorizes them.
type Cascader interface {
	CascadeDeleteByInjectionIDs(ctx context.Context, scope authz.CallerScope, injectionIDs []int) error
}

type cascader struct {
	db *gorm.DB
}

func NewCascader(db *gorm.DB) Cascader {
	return &cascader{db: db}
}

func (c *cascader) CascadeDeleteByInjectionIDs(ctx context.Context, scope authz.CallerScope, injectionIDs []int) error {
	if len(injectionIDs) == 0 {
		return nil
	}

	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&model.Execution{}).
			Where("datapack_id IN ? AND executions.status != ?", injectionIDs, consts.CommonDeleted)

		if !scope.IsAdmin {
			if len(scope.VisibleProjects) == 0 {
				return nil
			}
			query = query.
				Joins("JOIN tasks ON tasks.id = executions.task_id").
				Joins("JOIN traces ON traces.id = tasks.trace_id").
				Where("traces.project_id IN ?", scope.VisibleProjects)
		}

		var executionIDs []int
		if err := query.Pluck("executions.id", &executionIDs).Error; err != nil {
			return fmt.Errorf("failed to list executions for injection cascade: %w", err)
		}
		if len(executionIDs) == 0 {
			return nil
		}

		if err := tx.Where("execution_id IN ?", executionIDs).
			Delete(&model.ExecutionInjectionLabel{}).Error; err != nil {
			return fmt.Errorf("failed to delete execution labels: %w", err)
		}
		if err := tx.Model(&model.Execution{}).
			Where("id IN ? AND status != ?", executionIDs, consts.CommonDeleted).
			Update("status", consts.CommonDeleted).Error; err != nil {
			return fmt.Errorf("failed to soft-delete executions: %w", err)
		}
		return nil
	})
}
