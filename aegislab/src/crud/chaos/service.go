package chaos

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
)

var (
	ErrSystemNotFound        = errors.New("chaos: system not found")
	ErrSystemDisabled        = errors.New("chaos: system disabled")
	ErrPointNotFound         = errors.New("chaos: point not found")
	ErrPointNotActive        = errors.New("chaos: point not active")
	ErrCapabilityUnsupported = errors.New("chaos: capability not supported by executor")
	ErrIdempotencyMismatch   = errors.New("chaos: idempotency_key reused with different request body")
)

// Manager is the in-process facade the HTTP handler talks to. It owns the
// DB + Executor and exposes the operations §5 wires up in step 1.
//
// Named Manager rather than Service so it doesn't collide with the Service
// table model in this package.
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
	return s.DB.WithContext(ctx).Save(sys).Error
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

// CreateInjection is the singleton POST /v1beta/injections code path.
// It enforces idempotency on idempotency_key and derives executor_handle
// BEFORE Apply (ADR-0004).
type CreateInjectionInput struct {
	PointID        string
	Params         map[string]any
	IdempotencyKey string
	CallerMetadata map[string]any
	ExecutorPin    string
}

func (s *Manager) CreateInjection(ctx context.Context, in CreateInjectionInput) (*Injection, error) {
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("chaos: idempotency_key is required")
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

	var cap Capability
	if err := s.DB.WithContext(ctx).Where("name = ?", point.CapabilityName).Take(&cap).Error; err != nil {
		return nil, err
	}
	if cap.Status == CapDeprecated {
		return nil, ErrCapabilityUnsupported
	}

	supported := false
	for _, c := range s.Executor.SupportedCapabilities() {
		if c.Capability == cap.Name {
			supported = true
			break
		}
	}
	if !supported {
		return nil, ErrCapabilityUnsupported
	}

	sys, err := s.GetSystem(ctx, point.SystemName)
	if err != nil {
		return nil, err
	}
	if !sys.Enabled {
		return nil, ErrSystemDisabled
	}

	now := time.Now().UTC()
	id := ulid.Make().String()

	// Persist row in `pending` state with a placeholder handle, then Apply
	// and update. ADR-0004's "handle derived from key" lets a crash at any
	// point be recovered by re-Apply against the same CR name.
	row := Injection{
		ID:             id,
		PointID:        point.ID,
		Params:         in.Params,
		IdempotencyKey: in.IdempotencyKey,
		ExecutorName:   s.Executor.Name(),
		ExecutorHandle: "",
		Status:         StatusPending,
		CallerMetadata: in.CallerMetadata,
		Ts:             now,
	}
	if err := s.DB.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, err
	}

	handle, applyErr := s.Executor.Apply(ctx, cap.Name, in.IdempotencyKey, point.Target, in.Params)
	if applyErr != nil {
		fin := now
		updates := map[string]any{
			"status":      StatusFailed,
			"diagnostics": JSONMap{"error": applyErr.Error()},
			"started_at":  &now,
			"finished_at": &fin,
		}
		_ = s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", id).Updates(updates).Error
		row.Status = StatusFailed
		row.Diagnostics = JSONMap{"error": applyErr.Error()}
		row.StartedAt = &now
		row.FinishedAt = &fin
		return &row, applyErr
	}
	row.ExecutorHandle = handle
	row.Status = StatusRunning
	row.StartedAt = &now
	updates := map[string]any{
		"executor_handle": handle,
		"status":          StatusRunning,
		"started_at":      &now,
	}
	if err := s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *Manager) GetInjection(ctx context.Context, id string) (*Injection, error) {
	var inj Injection
	if err := s.DB.WithContext(ctx).Where("id = ?", id).Take(&inj).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPointNotFound
		}
		return nil, err
	}
	return &inj, nil
}

// DeleteInjection implements the ADR-0003 Destroy contract. Idempotent on
// id; terminal-state injections destroy the cluster artifact (no-op if
// already gone) and move to `cancelled` only if not already terminal.
func (s *Manager) DeleteInjection(ctx context.Context, id string) (*Injection, error) {
	inj, err := s.GetInjection(ctx, id)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	terminal := isTerminal(inj.Status)

	if inj.ExecutorHandle != "" {
		if dErr := s.Executor.Destroy(ctx, inj.ExecutorHandle); dErr != nil {
			updates := map[string]any{"destroy_error": dErr.Error()}
			_ = s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", id).Updates(updates).Error
			inj.DestroyError = dErr.Error()
			// ADR-0003: Destroy failure does not block status transition.
		} else {
			updates := map[string]any{"destroyed_at": &now}
			_ = s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", id).Updates(updates).Error
			inj.DestroyedAt = &now
		}
	}

	if !terminal {
		updates := map[string]any{
			"status":      StatusCancelled,
			"finished_at": &now,
		}
		if err := s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", id).Updates(updates).Error; err != nil {
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

func (s *Manager) ListCapabilities(ctx context.Context) ([]Capability, error) {
	var out []Capability
	if err := s.DB.WithContext(ctx).Order("name").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Manager) GetCapability(ctx context.Context, name string) (*Capability, error) {
	var c Capability
	if err := s.DB.WithContext(ctx).Where("name = ?", name).Take(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPointNotFound
		}
		return nil, err
	}
	return &c, nil
}
