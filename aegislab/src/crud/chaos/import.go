package chaos

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ReplaceScopeService = "service"
	ReplaceScopeSystem  = "system"
	ReplaceScopeNone    = "none"
)

type PointManifest struct {
	APIVersion string                `json:"apiVersion" yaml:"apiVersion"`
	Kind       string                `json:"kind"       yaml:"kind"`
	Metadata   PointManifestMetadata `json:"metadata"   yaml:"metadata"`
	Spec       PointManifestSpec     `json:"spec"       yaml:"spec"`
}

type PointManifestMetadata struct {
	System       string `json:"system"        yaml:"system"`
	Service      string `json:"service"       yaml:"service"`
	Instance     string `json:"instance"      yaml:"instance"`
	ChartVersion string `json:"chart_version" yaml:"chart_version"`
}

type PointManifestSpec struct {
	ReplaceScope string                `json:"replace_scope" yaml:"replace_scope"`
	Points       []PointManifestEntry  `json:"points"        yaml:"points"`
}

type PointManifestEntry struct {
	Capability     string         `json:"capability"               yaml:"capability"`
	Target         map[string]any `json:"target"                   yaml:"target"`
	ParamOverrides map[string]any `json:"param_overrides,omitempty" yaml:"param_overrides,omitempty"`
}

type ImportResult struct {
	Upserted   int      `json:"upserted"`
	Superseded int      `json:"superseded"`
	DryRun     bool     `json:"dry_run"`
	PointIDs   []string `json:"point_ids"`
}

// ImportPoints applies a PointManifest under §6 / ADR-0011 semantics.
// Serialises per (system, service, instance) via the import_locks table
// (UPSERT then SELECT … FOR UPDATE inside the tx).
func (s *Manager) ImportPoints(ctx context.Context, systemName string, m PointManifest, dryRun bool) (*ImportResult, error) {
	if err := validateManifest(systemName, &m); err != nil {
		return nil, err
	}
	if m.Spec.ReplaceScope == ReplaceScopeSystem {
		return nil, fmt.Errorf("chaos: replace_scope=system not implemented in step 1")
	}

	if _, err := s.GetSystem(ctx, systemName); err != nil {
		return nil, err
	}

	tx := s.DB.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	if err := s.acquireImportLock(tx, m); err != nil {
		return nil, err
	}

	svcID, err := s.upsertService(tx, m)
	if err != nil {
		return nil, err
	}

	caps, err := s.loadCapabilityNames(tx)
	if err != nil {
		return nil, err
	}

	out := &ImportResult{DryRun: dryRun}
	newIDs := make(map[string]struct{}, len(m.Spec.Points))

	for _, p := range m.Spec.Points {
		if _, ok := caps[p.Capability]; !ok {
			return nil, fmt.Errorf("chaos: unknown capability %q", p.Capability)
		}
		ident := PointIdentity{
			System: systemName, Service: m.Metadata.Service,
			Instance: m.Metadata.Instance, ChartVersion: m.Metadata.ChartVersion,
			Capability: p.Capability, Target: p.Target,
		}
		id, err := ServiceBoundPointID(ident)
		if err != nil {
			return nil, err
		}
		newIDs[id] = struct{}{}
		out.PointIDs = append(out.PointIDs, id)

		row := Point{
			ID:             id,
			SystemName:     systemName,
			ServiceID:      &svcID,
			CapabilityName: p.Capability,
			Target:         JSONMap(p.Target),
			ParamOverrides: JSONMap(p.ParamOverrides),
			Source:         "import",
			Status:         PointActive,
		}
		res := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"param_overrides", "status", "updated_at",
			}),
		}).Create(&row)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected > 0 {
			out.Upserted++
		}
	}

	if m.Spec.ReplaceScope == ReplaceScopeService {
		var stale []Point
		if err := tx.Where(
			"system_name = ? AND service_id = ? AND status = ?",
			systemName, svcID, PointActive,
		).Find(&stale).Error; err != nil {
			return nil, err
		}
		for _, p := range stale {
			if _, kept := newIDs[p.ID]; kept {
				continue
			}
			if err := tx.Model(&Point{}).Where("id = ?", p.ID).Updates(map[string]any{
				"status":     PointSuperseded,
				"updated_at": time.Now().UTC(),
			}).Error; err != nil {
				return nil, err
			}
			out.Superseded++
		}
	}

	if dryRun {
		return out, nil
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	committed = true
	return out, nil
}

func validateManifest(systemName string, m *PointManifest) error {
	if m.APIVersion != "aegis-chaos/v1beta" {
		return fmt.Errorf("chaos: unsupported manifest apiVersion %q", m.APIVersion)
	}
	if m.Kind != "PointManifest" {
		return fmt.Errorf("chaos: unsupported manifest kind %q", m.Kind)
	}
	if m.Metadata.System != systemName {
		return fmt.Errorf("chaos: manifest system %q does not match path %q", m.Metadata.System, systemName)
	}
	if m.Metadata.Service == "" {
		return fmt.Errorf("chaos: manifest metadata.service is required")
	}
	if m.Metadata.ChartVersion == "" {
		return fmt.Errorf("chaos: manifest metadata.chart_version is required")
	}
	if m.Metadata.Instance == "" {
		m.Metadata.Instance = "default"
	}
	switch m.Spec.ReplaceScope {
	case ReplaceScopeService, ReplaceScopeSystem, ReplaceScopeNone, "":
	default:
		return fmt.Errorf("chaos: invalid replace_scope %q", m.Spec.ReplaceScope)
	}
	return nil
}

// acquireImportLock upserts the lock row then takes a row lock so
// concurrent imports against the same (system, service, instance) block
// until the prior tx commits or rolls back. ADR-0011.
//
// SQLite tx serialisation gives equivalent behaviour for unit tests
// (the writer holds an exclusive lock for the whole tx); FOR UPDATE is
// a no-op there but doesn't error.
func (s *Manager) acquireImportLock(tx *gorm.DB, m PointManifest) error {
	now := time.Now().UTC()
	row := ImportLock{
		SystemName: m.Metadata.System, ServiceName: m.Metadata.Service,
		Instance: m.Metadata.Instance, LockedBy: "import", LockedAt: &now,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "system_name"}, {Name: "service_name"}, {Name: "instance"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"locked_by", "locked_at"}),
	}).Create(&row).Error; err != nil {
		return err
	}
	if tx.Dialector.Name() == "mysql" {
		var locked ImportLock
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"system_name = ? AND service_name = ? AND instance = ?",
			m.Metadata.System, m.Metadata.Service, m.Metadata.Instance,
		).Take(&locked).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Manager) upsertService(tx *gorm.DB, m PointManifest) (int64, error) {
	now := time.Now().UTC()
	var existing Service
	err := tx.Where(
		"system_name = ? AND name = ? AND instance = ? AND chart_version = ?",
		m.Metadata.System, m.Metadata.Service, m.Metadata.Instance, m.Metadata.ChartVersion,
	).Take(&existing).Error
	if err == nil {
		existing.LastSeenAt = now
		existing.Status = ServiceActive
		if err := tx.Save(&existing).Error; err != nil {
			return 0, err
		}
		return existing.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	row := Service{
		SystemName:   m.Metadata.System,
		Name:         m.Metadata.Service,
		Instance:     m.Metadata.Instance,
		ChartVersion: m.Metadata.ChartVersion,
		Status:       ServiceActive,
		DiscoveredAt: now,
		LastSeenAt:   now,
	}
	if err := tx.Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

func (s *Manager) loadCapabilityNames(tx *gorm.DB) (map[string]struct{}, error) {
	var rows []Capability
	if err := tx.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		out[r.Name] = struct{}{}
	}
	return out, nil
}
