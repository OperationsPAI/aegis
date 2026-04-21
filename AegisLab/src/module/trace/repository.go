package trace

import (
	"aegis/consts"
	"aegis/model"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetTraceByID(traceID string) (*model.Trace, error) {
	var trace model.Trace
	if err := r.db.Model(&model.Trace{}).
		Preload("Project").
		Preload("Tasks", func(db *gorm.DB) *gorm.DB {
			return db.Order("level ASC, sequence ASC")
		}).
		Where("id = ? AND status != ?", traceID, consts.CommonDeleted).
		First(&trace).Error; err != nil {
		return nil, err
	}
	return &trace, nil
}

// ListInFlightTaskIDsByTrace returns the IDs of Pending/Running/Rescheduled
// tasks for the given trace, i.e. tasks that might still be sitting in redis
// queues or executing in the cluster. Terminal tasks (Completed/Error/Cancelled)
// are skipped.
func (r *Repository) ListInFlightTaskIDsByTrace(traceID string) ([]string, error) {
	var ids []string
	if err := r.db.Model(&model.Task{}).
		Where("trace_id = ? AND status != ? AND state IN ?",
			traceID, consts.CommonDeleted,
			[]consts.TaskState{consts.TaskPending, consts.TaskRescheduled, consts.TaskRunning}).
		Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("failed to list in-flight tasks for trace %s: %w", traceID, err)
	}
	return ids, nil
}

// MarkTraceCancelled transitions the trace to the Cancelled terminal state
// and stamps end_time. It also marks any non-terminal task rows as Cancelled
// so that in-flight queue consumers observe the state change on load.
func (r *Repository) MarkTraceCancelled(traceID string) error {
	now := time.Now()
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Trace{}).
			Where("id = ? AND status != ?", traceID, consts.CommonDeleted).
			Updates(map[string]any{
				"state":      consts.TraceCancelled,
				"last_event": consts.EventTraceCancelled,
				"end_time":   &now,
				"updated_at": now,
			}).Error; err != nil {
			return fmt.Errorf("failed to cancel trace %s: %w", traceID, err)
		}
		if err := tx.Model(&model.Task{}).
			Where("trace_id = ? AND status != ? AND state IN ?",
				traceID, consts.CommonDeleted,
				[]consts.TaskState{consts.TaskPending, consts.TaskRescheduled, consts.TaskRunning}).
			Updates(map[string]any{
				"state":      consts.TaskCancelled,
				"updated_at": now,
			}).Error; err != nil {
			return fmt.Errorf("failed to cancel tasks for trace %s: %w", traceID, err)
		}
		return nil
	})
}

func (r *Repository) ListTraces(limit, offset int, filterOptions *ListTraceFilters) ([]model.Trace, int64, error) {
	var (
		traces []model.Trace
		total  int64
	)

	query := r.db.Model(&model.Trace{}).Preload("Project")
	if filterOptions.TraceType != nil {
		query = query.Where("type = ?", *filterOptions.TraceType)
	}
	if filterOptions.GroupID != "" {
		query = query.Where("group_id = ?", filterOptions.GroupID)
	}
	if filterOptions.ProjectID > 0 {
		query = query.Where("project_id = ?", filterOptions.ProjectID)
	}
	if filterOptions.State != nil {
		query = query.Where("state = ?", *filterOptions.State)
	}
	if filterOptions.Status != nil {
		query = query.Where("status = ?", *filterOptions.Status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count traces: %w", err)
	}
	if err := query.Limit(limit).Offset(offset).Order("created_at DESC").Find(&traces).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list traces: %w", err)
	}
	return traces, total, nil
}
