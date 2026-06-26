package sso

import (
	"context"
	"errors"
	"time"

	"aegis/platform/consts"

	"gorm.io/gorm"
)

type FederationRepository struct {
	db *gorm.DB
}

func NewFederationRepository(db *gorm.DB) *FederationRepository {
	return &FederationRepository{db: db}
}

func (r *FederationRepository) FindProviderByName(ctx context.Context, name string) (*IdentityProvider, error) {
	var p IdentityProvider
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, consts.ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (r *FederationRepository) ListEnabledProviders(ctx context.Context) ([]IdentityProvider, error) {
	var out []IdentityProvider
	if err := r.db.WithContext(ctx).Where("enabled = ?", true).Order("name ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *FederationRepository) CreateProvider(ctx context.Context, p *IdentityProvider) error {
	return r.db.WithContext(ctx).Create(p).Error
}

func (r *FederationRepository) UpdateProvider(ctx context.Context, p *IdentityProvider) error {
	return r.db.WithContext(ctx).Save(p).Error
}

func (r *FederationRepository) DeleteProvider(ctx context.Context, name string) error {
	res := r.db.WithContext(ctx).Where("name = ?", name).Delete(&IdentityProvider{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return consts.ErrNotFound
	}
	return nil
}

func (r *FederationRepository) FindIdentity(ctx context.Context, provider, externalSub string) (*UserIdentity, error) {
	var ui UserIdentity
	if err := r.db.WithContext(ctx).Where("provider = ? AND external_sub = ?", provider, externalSub).First(&ui).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, consts.ErrNotFound
		}
		return nil, err
	}
	return &ui, nil
}

func (r *FederationRepository) FindIdentityByUserAndProvider(ctx context.Context, userID int, provider string) (*UserIdentity, error) {
	var ui UserIdentity
	if err := r.db.WithContext(ctx).Where("user_id = ? AND provider = ?", userID, provider).First(&ui).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, consts.ErrNotFound
		}
		return nil, err
	}
	return &ui, nil
}

func (r *FederationRepository) LinkIdentity(ctx context.Context, identity *UserIdentity) error {
	return r.db.WithContext(ctx).Create(identity).Error
}

func (r *FederationRepository) UpdateIdentityMetadata(ctx context.Context, id int64, metadata string) error {
	return r.db.WithContext(ctx).Model(&UserIdentity{}).Where("id = ?", id).Update("metadata", metadata).Error
}

func (r *FederationRepository) UpdateLastUsed(ctx context.Context, id int64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&UserIdentity{}).Where("id = ?", id).Update("last_used_at", &now).Error
}

func (r *FederationRepository) ListUserIdentities(ctx context.Context, userID int) ([]UserIdentity, error) {
	var out []UserIdentity
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("linked_at ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *FederationRepository) UnlinkIdentity(ctx context.Context, id int64) error {
	res := r.db.WithContext(ctx).Delete(&UserIdentity{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return consts.ErrNotFound
	}
	return nil
}
