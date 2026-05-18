package pedestal

import (
	"context"
	"fmt"

	"aegis/platform/model"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// GetHelmConfigByContainerVersionID returns the helm_configs row bound to the
// given container version. Returns gorm.ErrRecordNotFound if absent.
func (r *Repository) GetHelmConfigByContainerVersionID(ctx context.Context, versionID int) (*model.HelmConfig, error) {
	var cfg model.HelmConfig
	if err := r.db.WithContext(ctx).Where("container_version_id = ?", versionID).First(&cfg).Error; err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetContainerVersionByID fetches a ContainerVersion together with its
// parent Container and HelmConfig (with DynamicValues preloaded) so the
// admin runtime endpoints can resolve a pedestal install from a single
// container_version_id. Returns gorm.ErrRecordNotFound when the version
// doesn't exist (the HelmConfig preload itself is tolerant — a row without
// a helm_configs link returns the version with HelmConfig=nil so callers
// can distinguish "unknown version" from "version without helm config").
func (r *Repository) GetContainerVersionByID(ctx context.Context, id int) (*model.ContainerVersion, error) {
	var v model.ContainerVersion
	err := r.db.WithContext(ctx).
		Preload("Container").
		Preload("HelmConfig").
		Preload("HelmConfig.DynamicValues").
		Where("id = ? AND status >= 0", id).
		First(&v).Error
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// UpsertHelmConfig creates a new row if versionID has none, else updates
// the existing row in place. Returns the (fresh) row.
func (r *Repository) UpsertHelmConfig(ctx context.Context, versionID int, fields *model.HelmConfig) (*model.HelmConfig, error) {
	existing, err := r.GetHelmConfigByContainerVersionID(ctx, versionID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("query existing helm config: %w", err)
	}
	if existing != nil && existing.ID != 0 {
		existing.ChartName = fields.ChartName
		existing.Version = fields.Version
		existing.RepoURL = fields.RepoURL
		existing.RepoName = fields.RepoName
		existing.ValueFile = fields.ValueFile
		existing.LocalPath = fields.LocalPath
		if err := r.db.WithContext(ctx).Save(existing).Error; err != nil {
			return nil, fmt.Errorf("update helm config: %w", err)
		}
		return existing, nil
	}
	fields.ContainerVersionID = versionID
	if err := r.db.WithContext(ctx).Create(fields).Error; err != nil {
		return nil, fmt.Errorf("create helm config: %w", err)
	}
	return fields, nil
}
