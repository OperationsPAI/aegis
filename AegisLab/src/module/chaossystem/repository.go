package chaossystem

import (
	"errors"
	"fmt"

	"aegis/platform/consts"
	"aegis/platform/model"

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

// DB exposes the underlying *gorm.DB for callers that need to run operations
// across multiple tables (e.g. the reseed service, which diffs containers
// + container_versions + helm_configs + dynamic_configs in one pass).
func (r *Repository) DB() *gorm.DB { return r.db }

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

// GetPedestalHelmConfigByName returns the HelmConfig for a pedestal
// ContainerVersion whose Container.Name equals the system short code. When
// versionName is "" the highest-versioned active container_version wins;
// when set, the exact match is returned. The chaossystem module consumes
// this to expose GET /systems/by-name/:name/chart[?version=…] — clients use
// it to pull the chart tgz without needing to walk containers → versions →
// helm_configs themselves.
//
// The DynamicValues many-to-many is preloaded inline so the caller sees the
// current parameter_configs.default_value rows for the requested version
// (issue #190: the endpoint must reflect live helm_config_values, not a
// cached snapshot).
//
// Returns (nil, nil, nil) when the system has no pedestal container, no
// matching version, or no helm config — the HTTP layer maps that to 404.
func (r *Repository) GetPedestalHelmConfigByName(name, versionName string) (*model.HelmConfig, *model.ContainerVersion, error) {
	var container model.Container
	if err := r.db.Where("name = ? AND type = ? AND status >= 0", name, consts.ContainerTypePedestal).
		First(&container).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to find pedestal container %s: %w", name, err)
	}

	var version model.ContainerVersion
	q := r.db.Where("container_id = ? AND status >= 0", container.ID)
	if versionName != "" {
		// Exact-version lookup. We match on the user-visible Name column
		// instead of the (major,minor,patch) triple so callers can't
		// accidentally collide with a different ContainerVersion that
		// happens to parse to the same semver triple.
		q = q.Where("name = ?", versionName)
	} else {
		q = q.Order("name_major DESC, name_minor DESC, name_patch DESC, id DESC")
	}
	if err := q.First(&version).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to find active version for pedestal %s: %w", name, err)
	}

	var helm model.HelmConfig
	if err := r.db.Preload("DynamicValues").Where("container_version_id = ?", version.ID).First(&helm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to find helm config for version %d: %w", version.ID, err)
	}

	return &helm, &version, nil
}

// ListPrerequisites returns every prerequisite for the given system, sorted
// by kind then name so CLI output is stable. An empty result is not an
// error — a system without prereqs is the common case.
func (r *Repository) ListPrerequisites(systemName string) ([]model.SystemPrerequisite, error) {
	var rows []model.SystemPrerequisite
	if err := r.db.Where("system_name = ?", systemName).
		Order("kind ASC, name ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list prerequisites for %s: %w", systemName, err)
	}
	return rows, nil
}

// GetPrerequisiteByID fetches one row by primary key, scoped to the given
// system so a mismatched (system,id) pair returns NotFound instead of
// silently leaking another system's row.
func (r *Repository) GetPrerequisiteByID(systemName string, id int) (*model.SystemPrerequisite, error) {
	var row model.SystemPrerequisite
	err := r.db.Where("id = ? AND system_name = ?", id, systemName).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get prerequisite %d for %s: %w", id, systemName, err)
	}
	return &row, nil
}

// UpdatePrerequisiteStatus flips the status column. Used by aegisctl after a
// successful `helm upgrade --install`.
func (r *Repository) UpdatePrerequisiteStatus(id int, status string) error {
	res := r.db.Model(&model.SystemPrerequisite{}).
		Where("id = ?", id).
		Update("status", status)
	if res.Error != nil {
		return fmt.Errorf("update prerequisite %d status: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// We intentionally do not expose a "delete config" helper. Removing a system
// is modeled as setting its status to CommonDeleted via the etcd write path so
// history/audit stays consistent.
