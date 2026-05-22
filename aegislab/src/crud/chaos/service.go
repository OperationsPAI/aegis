package chaos

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

var (
	ErrSystemNotFound        = errors.New("chaos: system not found")
	ErrSystemDisabled        = errors.New("chaos: system disabled")
	ErrPointNotFound         = errors.New("chaos: point not found")
	ErrPointNotActive        = errors.New("chaos: point not active")
	ErrInjectionNotFound     = errors.New("chaos: injection not found")
	ErrBatchNotFound         = errors.New("chaos: injection batch not found")
	ErrCapabilityNotFound    = errors.New("chaos: capability not found")
	ErrCapabilityUnsupported = errors.New("chaos: capability not supported by executor")
	ErrIdempotencyMismatch   = errors.New("chaos: idempotency_key reused with different request body")
	ErrBatchEmpty            = errors.New("chaos: injection batch requires at least one child")
	ErrSystemAtCapacity      = errors.New("chaos: system at max concurrent injections")
)

// Manager is named after the role rather than "Service" so it doesn't
// collide with the Service table model in this package.
type Manager struct {
	DB       *gorm.DB
	Executor Executor
}

func NewManager(db *gorm.DB, exec Executor) *Manager {
	return &Manager{DB: db, Executor: exec}
}

func (s *Manager) UpsertSystem(ctx context.Context, sys *System) error {
	if sys.Name == "" {
		return fmt.Errorf("chaos: system.name is required")
	}
	if sys.MaxConcurrentInjections == 0 {
		sys.MaxConcurrentInjections = 5
	}

	// Split insert vs. update paths: gorm's Save() leaves CreatedAt at
	// zero on the UPDATE branch when the input struct has the time-zero
	// value, and MySQL strict mode rejects '0000-00-00 00:00:00' with
	// Error 1292. The on-conflict UPDATE here explicitly preserves the
	// existing created_at and only writes the mutable fields.
	db := s.DB.WithContext(ctx)
	var existing System
	err := db.Where("name = ?", sys.Name).Take(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		now := time.Now().UTC()
		sys.CreatedAt = now
		sys.UpdatedAt = now
		return db.Create(sys).Error
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	sys.CreatedAt = existing.CreatedAt
	sys.UpdatedAt = now
	return db.Model(&System{}).Where("name = ?", sys.Name).Updates(map[string]any{
		"ns_pattern":                sys.NsPattern,
		"app_label_key":             sys.AppLabelKey,
		"enabled":                   sys.Enabled,
		"max_concurrent_injections": sys.MaxConcurrentInjections,
		"updated_at":                now,
	}).Error
}

func (s *Manager) GetSystem(ctx context.Context, name string) (*System, error) {
	var sys System
	err := s.DB.WithContext(ctx).Where("name = ?", name).Take(&sys).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSystemNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sys, nil
}

type CreateInjectionInput struct {
	PointID        string
	Namespace      string
	Params         map[string]any
	IdempotencyKey string
	CallerMetadata map[string]any
	ExecutorPin    string
}

func (s *Manager) CreateInjection(ctx context.Context, in CreateInjectionInput) (*Injection, error) {
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("chaos: idempotency_key is required")
	}
	if in.Namespace == "" {
		return nil, fmt.Errorf("chaos: namespace is required")
	}

	var existing Injection
	err := s.DB.WithContext(ctx).Where("idempotency_key = ?", in.IdempotencyKey).Take(&existing).Error
	if err == nil {
		if existing.PointID != in.PointID {
			return nil, ErrIdempotencyMismatch
		}
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var point Point
	if err := s.DB.WithContext(ctx).Where("id = ?", in.PointID).Take(&point).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPointNotFound
		}
		return nil, err
	}
	if point.Status != PointActive {
		return nil, ErrPointNotActive
	}

	var capRow Capability
	if err := s.DB.WithContext(ctx).Where("name = ?", point.CapabilityName).Take(&capRow).Error; err != nil {
		return nil, err
	}
	if capRow.Status == CapDeprecated {
		return nil, ErrCapabilityUnsupported
	}

	supported := false
	for _, c := range s.Executor.SupportedCapabilities() {
		if c.Capability == capRow.Name {
			supported = true
			break
		}
	}
	if !supported {
		return nil, ErrCapabilityUnsupported
	}

	effectiveParams := mergeParams(in.Params, map[string]any(point.ParamOverrides))
	sc := newSchemaCompiler()
	paramSchema, err := sc.forParams(&capRow)
	if err != nil {
		return nil, err
	}
	if err := validateInstance(paramSchema, effectiveParams, "params"); err != nil {
		return nil, err
	}

	sys, err := s.GetSystem(ctx, point.SystemName)
	if err != nil {
		return nil, err
	}
	if !sys.Enabled {
		return nil, ErrSystemDisabled
	}
	if err := s.checkSystemCapacity(ctx, sys); err != nil {
		return nil, err
	}
	sysCtx := SystemContext{Name: sys.Name, AppLabelKey: sys.AppLabelKey}

	// ADR-0004: derive the executor_handle BEFORE Apply so a crash mid-Apply
	// leaves a recoverable row — the persisted handle is valid regardless of
	// whether the CR was created, because the CR name is deterministic.
	target := map[string]any(point.Target)
	handle, err := s.Executor.DeriveHandle(capRow.Name, in.IdempotencyKey, in.Namespace, target)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	id := ulid.Make().String()
	row := Injection{
		ID:             id,
		PointID:        point.ID,
		Params:         JSONMap(effectiveParams),
		IdempotencyKey: in.IdempotencyKey,
		ExecutorName:   s.Executor.Name(),
		ExecutorHandle: handle,
		Status:         StatusPending,
		CallerMetadata: in.CallerMetadata,
		TaskID:         taskIDFromMeta(in.CallerMetadata),
		Ts:             now,
	}
	if err := s.DB.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, err
	}

	applyAt := time.Now().UTC()
	if applyErr := s.Executor.Apply(ctx, sysCtx, capRow.Name, handle, target, effectiveParams); applyErr != nil {
		fin := applyAt
		updates := map[string]any{
			"status":      StatusFailed,
			"diagnostics": JSONMap{"error": applyErr.Error()},
			"started_at":  &applyAt,
			"finished_at": &fin,
		}
		if uerr := s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", id).Updates(updates).Error; uerr != nil {
			logrus.Warnf("chaos: failed to record Apply failure for %s: %v", id, uerr)
		}
		row.Status = StatusFailed
		row.Diagnostics = JSONMap{"error": applyErr.Error()}
		row.StartedAt = &applyAt
		row.FinishedAt = &fin
		return &row, applyErr
	}

	row.Status = StatusRunning
	row.StartedAt = &applyAt
	if err := s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", id).Updates(map[string]any{
		"status":     StatusRunning,
		"started_at": &applyAt,
	}).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *Manager) GetInjection(ctx context.Context, id string) (*Injection, error) {
	var inj Injection
	if err := s.DB.WithContext(ctx).Where("id = ?", id).Take(&inj).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInjectionNotFound
		}
		return nil, err
	}
	return &inj, nil
}

// DeleteInjection implements the ADR-0003 Destroy contract. Idempotent on
// id; non-terminal injections move to `cancelled`. Destroy failure is
// recorded in diagnostics but does NOT block the status transition.
func (s *Manager) DeleteInjection(ctx context.Context, id string) (*Injection, error) {
	inj, err := s.GetInjection(ctx, id)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	terminal := isTerminal(inj.Status)

	if inj.ExecutorHandle != "" && inj.DestroyedAt == nil {
		if dErr := s.Executor.Destroy(ctx, inj.ExecutorHandle); dErr != nil {
			if uerr := s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", id).
				Updates(map[string]any{"destroy_error": dErr.Error()}).Error; uerr != nil {
				logrus.Warnf("chaos: failed to record Destroy error for %s: %v", id, uerr)
			}
			inj.DestroyError = dErr.Error()
		} else {
			if uerr := s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", id).
				Updates(map[string]any{"destroyed_at": &now, "destroy_error": ""}).Error; uerr != nil {
				logrus.Warnf("chaos: failed to record Destroy success for %s: %v", id, uerr)
			}
			inj.DestroyedAt = &now
			inj.DestroyError = ""
		}
	}

	if !terminal {
		if err := s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", id).Updates(map[string]any{
			"status":      StatusCancelled,
			"finished_at": &now,
		}).Error; err != nil {
			return nil, err
		}
		inj.Status = StatusCancelled
		inj.FinishedAt = &now
	}
	return inj, nil
}

func isTerminal(s string) bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// TaskInjectionRef is the resolved (id, isBatch) pair for a caller-supplied
// task_id.
type TaskInjectionRef struct {
	ID      string
	IsBatch bool
}

// taskIDFromMeta returns "" for non-string values — the by-task DELETE then
// can't find the row, which is the correct outcome for callers that didn't
// stamp a usable task_id.
func taskIDFromMeta(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	v, ok := meta["task_id"].(string)
	if !ok {
		return ""
	}
	return v
}

// LookupTaskInjection resolves a caller-supplied task_id into a (batch|injection)
// destroy target. Lookup is singleton-first to handle the hybrid-batch case:
// when the CLI / external caller stamps the same task_id on both the batch
// envelope AND every child, a singleton hit whose row carries batch_id != NULL
// means the caller wants the whole batch destroyed, not one arbitrary child.
func (s *Manager) LookupTaskInjection(ctx context.Context, taskID string) (TaskInjectionRef, error) {
	if taskID == "" {
		return TaskInjectionRef{}, ErrInjectionNotFound
	}
	db := s.DB.WithContext(ctx)

	var child Injection
	err := db.Select("id", "batch_id").
		Where("task_id = ?", taskID).
		Order("ts ASC").Limit(1).Take(&child).Error
	if err == nil {
		if child.BatchID != nil && *child.BatchID != "" {
			return TaskInjectionRef{ID: *child.BatchID, IsBatch: true}, nil
		}
		return TaskInjectionRef{ID: child.ID, IsBatch: false}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return TaskInjectionRef{}, err
	}

	// Singleton miss can still mean a batch was created with zero children that
	// landed in chaos_injections (e.g. all children rejected pre-insert). Fall
	// through to the batch table on its own indexed task_id column.
	var batchID string
	err = db.Model(&InjectionBatch{}).
		Where("task_id = ?", taskID).
		Limit(1).Pluck("id", &batchID).Error
	if err != nil {
		return TaskInjectionRef{}, err
	}
	if batchID == "" {
		return TaskInjectionRef{}, ErrInjectionNotFound
	}
	return TaskInjectionRef{ID: batchID, IsBatch: true}, nil
}

func (s *Manager) ListCapabilities(ctx context.Context) ([]Capability, error) {
	var out []Capability
	if err := s.DB.WithContext(ctx).Order("name").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// checkSystemCapacity counts non-terminal injections scoped to the given
// system (joined via chaos_points.system_name) and refuses to accept a
// new one when the system is at its MaxConcurrentInjections limit. A
// zero limit is treated as unlimited.
func (s *Manager) checkSystemCapacity(ctx context.Context, sys *System) error {
	if sys.MaxConcurrentInjections <= 0 {
		return nil
	}
	var inFlight int64
	err := s.DB.WithContext(ctx).Model(&Injection{}).
		Joins("JOIN chaos_points ON chaos_points.id = chaos_injections.point_id").
		Where("chaos_points.system_name = ?", sys.Name).
		Where("chaos_injections.status IN ?", []string{StatusPending, StatusRunning}).
		Count(&inFlight).Error
	if err != nil {
		return err
	}
	if inFlight >= int64(sys.MaxConcurrentInjections) {
		return ErrSystemAtCapacity
	}
	return nil
}

func (s *Manager) GetCapability(ctx context.Context, name string) (*Capability, error) {
	var c Capability
	if err := s.DB.WithContext(ctx).Where("name = ?", name).Take(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCapabilityNotFound
		}
		return nil, err
	}
	return &c, nil
}
