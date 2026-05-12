package sso

import (
	"errors"

	"aegis/platform/consts"
	"aegis/platform/model"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Count() (int64, error) {
	var n int64
	err := r.db.Model(&model.OIDCClient{}).
		Where("status >= 0").
		Count(&n).Error
	return n, err
}

func (r *Repository) Create(c *model.OIDCClient) error {
	return r.db.Create(c).Error
}

func (r *Repository) GetByClientID(clientID string) (*model.OIDCClient, error) {
	var c model.OIDCClient
	if err := r.db.Where("client_id = ? AND status >= 0", clientID).First(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, consts.ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *Repository) GetByID(id int) (*model.OIDCClient, error) {
	var c model.OIDCClient
	if err := r.db.Where("id = ? AND status >= 0", id).First(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, consts.ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *Repository) List(serviceFilter string) ([]model.OIDCClient, error) {
	q := r.db.Model(&model.OIDCClient{}).Where("status >= 0")
	if serviceFilter != "" {
		q = q.Where("service = ?", serviceFilter)
	}
	var out []model.OIDCClient
	if err := q.Order("id ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repository) Update(c *model.OIDCClient) error {
	return r.db.Save(c).Error
}

func (r *Repository) SoftDelete(id int) error {
	res := r.db.Model(&model.OIDCClient{}).
		Where("id = ? AND status >= 0", id).
		Update("status", consts.CommonDeleted)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return consts.ErrNotFound
	}
	return nil
}
