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
	ErrCapabilityNotFound    = errors.New("chaos: capability not found")
	ErrCapabilityUnsupported = errors.New("chaos: capability not supported by executor")
	ErrIdempotencyMismatch   = errors.New("chaos: idempotency_key reused with different request body")
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

	sys, err := s.GetSystem(ctx, point.SystemName)
	if err != nil {
		return nil, err
	}
	if !sys.Enabled {
		return nil, ErrSystemDisabled
	}

	// ADR-0004: derive the executor_handle BEFORE Apply so a crash mid-Apply
	// leaves a recoverable row — the persisted handle is valid regardless of
	// whether the CR was created, because the CR name is deterministic.
	target := map[string]any(point.Target)
	handle, err := s.Executor.DeriveHandle(capRow.Name, in.IdempotencyKey, target)
	if err != nil {
		return nil, err
	}

	params := in.Params
	if params == nil {
		params = JSONMap{}
	}
	now := time.Now().UTC()
	id := ulid.Make().String()
	row := Injection{
		ID:             id,
		PointID:        point.ID,
		Params:         params,
		IdempotencyKey: in.IdempotencyKey,
		ExecutorName:   s.Executor.Name(),
		ExecutorHandle: handle,
		Status:         StatusPending,
		CallerMetadata: in.CallerMetadata,
		Ts:             now,
	}
	if err := s.DB.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, err
	}

	applyAt := time.Now().UTC()
	if applyErr := s.Executor.Apply(ctx, capRow.Name, handle, target, in.Params); applyErr != nil {
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
			return nil, ErrCapabilityNotFound
		}
		return nil, err
	}
	return &c, nil
}
