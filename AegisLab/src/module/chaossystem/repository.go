package chaossystem

import (
	"errors"
	"fmt"

	"aegis/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository bundles the DB access the chaossystem module needs post-issue-75.
// Reads of "system" state live in etcd/Viper; this repository is used only for
// the dynamic_config / config_history / system_metadata tables.
type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// GetConfigByKey returns the DynamicConfig row for the given key. Returns a
// (nil, nil) tuple when the row does not exist — callers treat the absence
// as "system has no state yet".
func (r *Repository) GetConfigByKey(key string) (*model.DynamicConfig, error) {
	var cfg model.DynamicConfig
	if err := r.db.Where("config_key = ?", key).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find config with key %s: %w", key, err)
	}
	return &cfg, nil
}

// ListSystemConfigs returns every DynamicConfig row whose key starts with
// "injection.system.". The caller groups them by system name.
func (r *Repository) ListSystemConfigs() ([]model.DynamicConfig, error) {
	var configs []model.DynamicConfig
	if err := r.db.Where("config_key LIKE ?", "injection.system.%").
		Order("config_key ASC").Find(&configs).Error; err != nil {
		return nil, fmt.Errorf("failed to list system configs: %w", err)
	}
	return configs, nil
}

// CreateConfig inserts a new DynamicConfig row.
func (r *Repository) CreateConfig(cfg *model.DynamicConfig) error {
	if err := r.db.Create(cfg).Error; err != nil {
		return fmt.Errorf("failed to create dynamic config %s: %w", cfg.Key, err)
	}
	return nil
}

// WriteHistory inserts a ConfigHistory entry.
func (r *Repository) WriteHistory(entry *model.ConfigHistory) error {
	if err := r.db.Create(entry).Error; err != nil {
		return fmt.Errorf("failed to create config history: %w", err)
	}
	return nil
}

// SaveConfig writes an existing DynamicConfig (upsert semantics via GORM Save).
func (r *Repository) SaveConfig(cfg *model.DynamicConfig) error {
	if err := r.db.Save(cfg).Error; err != nil {
		return fmt.Errorf("failed to save dynamic config %s: %w", cfg.Key, err)
	}
	return nil
}

// GetSystemMetadataByID returns a SystemMetadata row by its primary key; used
// by the list endpoints to surface a stable ID in the API response.
func (r *Repository) GetSystemMetadataByID(id int) (*model.SystemMetadata, error) {
	var meta model.SystemMetadata
	if err := r.db.Where("id = ?", id).First(&meta).Error; err != nil {
		return nil, fmt.Errorf("failed to find system metadata with id %d: %w", id, err)
	}
	return &meta, nil
}

func (r *Repository) UpsertSystemMetadata(meta *model.SystemMetadata) error {
	if err := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "system_name"}, {Name: "metadata_type"}, {Name: "service_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"data", "updated_at"}),
	}).Create(meta).Error; err != nil {
		var existing model.SystemMetadata
		if findErr := r.db.Where("system_name = ? AND metadata_type = ? AND service_name = ?",
			meta.SystemName, meta.MetadataType, meta.ServiceName).First(&existing).Error; findErr == nil {
			return r.db.Model(&existing).Updates(map[string]any{"data": meta.Data}).Error
		}
		return fmt.Errorf("failed to upsert system metadata: %w", err)
	}
	return nil
}

func (r *Repository) ListSystemMetadata(systemName, metadataType string) ([]model.SystemMetadata, error) {
	var metas []model.SystemMetadata
	query := r.db.Where("system_name = ?", systemName)
	if metadataType != "" {
		query = query.Where("metadata_type = ?", metadataType)
	}
	if err := query.Find(&metas).Error; err != nil {
		return nil, fmt.Errorf("failed to list system metadata: %w", err)
	}
	return metas, nil
}

// We intentionally do not expose a "delete config" helper. Removing a system
// is modeled as setting its status to CommonDeleted via the etcd write path so
// history/audit stays consistent.
