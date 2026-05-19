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
	Inserted   int      `json:"inserted"`
	Updated    int      `json:"updated"`
	Superseded int      `json:"superseded"`
	DryRun     bool     `json:"dry_run"`
	PointIDs   []string `json:"point_ids"`
}

// ImportPoints applies a PointManifest under §6 / ADR-0011 semantics.
// Serialises per (system, service, instance) via the import_locks table.
func (s *Manager) ImportPoints(ctx context.Context, systemName string, m PointManifest, dryRun bool) (*ImportResult, error) {
	if err := validateManifest(systemName, m); err != nil {
		return nil, err
	}

	tx := s.DB.WithContext(ctx).Begin()
	defer func() {
		if dryRun {
			tx.Rollback()
		}
	}()

	if _, err := s.GetSystem(ctx, systemName); err != nil {
		tx.Rollback()
		return nil, err
	}

	if err := s.acquireImportLock(tx, m); err != nil {
		tx.Rollback()
		return nil, err
	}

	svc, err := s.upsertService(tx, m)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	caps, err := s.loadCapabilityNames(tx)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	out := &ImportResult{DryRun: dryRun}
	newIDs := make(map[string]struct{}, len(m.Spec.Points))

	for _, p := range m.Spec.Points {
		if _, ok := caps[p.Capability]; !ok {
			tx.Rollback()
			return nil, fmt.Errorf("chaos: unknown capability %q", p.Capability)
		}
		ident := PointIdentity{
			System: systemName, Service: m.Metadata.Service,
			Instance: m.Metadata.Instance, ChartVersion: m.Metadata.ChartVersion,
			Capability: p.Capability, Target: p.Target,
		}
		id, err := ServiceBoundPointID(ident)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		newIDs[id] = struct{}{}
		out.PointIDs = append(out.PointIDs, id)

		row := Point{
			ID:             id,
			SystemName:     systemName,
			ServiceID:      &svc.ID,
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
			tx.Rollback()
			return nil, res.Error
		}
		if res.RowsAffected > 0 {
			out.Inserted++
		}
	}

	if m.Spec.ReplaceScope == ReplaceScopeService {
		// Mark Points absent from the payload as superseded for this
		// (system, service_id) scope.
		var stale []Point
		if err := tx.Where(
			"system_name = ? AND service_id = ? AND status = ?",
			systemName, svc.ID, PointActive,
		).Find(&stale).Error; err != nil {
			tx.Rollback()
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
				tx.Rollback()
				return nil, err
			}
			out.Superseded++
		}
	}

	if dryRun {
		// rolled back in deferred
		return out, nil
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return out, nil
}

func validateManifest(systemName string, m PointManifest) error {
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

func (s *Manager) acquireImportLock(tx *gorm.DB, m PointManifest) error {
	now := time.Now().UTC()
	row := ImportLock{
		SystemName: m.Metadata.System, ServiceName: m.Metadata.Service,
		Instance: m.Metadata.Instance, LockedBy: "import", LockedAt: &now,
	}
	// PK on (system, service, instance) — re-import for the same triple
	// just refreshes the lock timestamp within the same tx.
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "system_name"}, {Name: "service_name"}, {Name: "instance"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"locked_by", "locked_at"}),
	}).Create(&row).Error
}

func (s *Manager) upsertService(tx *gorm.DB, m PointManifest) (*Service2, error) {
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
			return nil, err
		}
		return &Service2{ID: existing.ID}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
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
		return nil, err
	}
	return &Service2{ID: row.ID}, nil
}

// Service2 carries just the id we need post-upsert; using the model type
// directly forces awkward naming because the package itself is `chaos`
// and the type is `Service`.
type Service2 struct{ ID int64 }

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
