package share

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

type Repository struct {
	DB *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{DB: db} }

func (r *Repository) Create(ctx context.Context, link *ShareLink) error {
	return r.DB.WithContext(ctx).Create(link).Error
}

func (r *Repository) FindByCode(ctx context.Context, code string) (*ShareLink, error) {
	var row ShareLink
	err := r.DB.WithContext(ctx).Where("short_code = ?", code).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrShareNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// IncrementViewCount atomically bumps view_count and returns the new
// value, so the caller can check it against MaxViews without a separate
// SELECT race.
func (r *Repository) IncrementViewCount(ctx context.Context, id int64) (int, error) {
	res := r.DB.WithContext(ctx).
		Model(&ShareLink{}).
		Where("id = ?", id).
		UpdateColumn("view_count", gorm.Expr("view_count + 1"))
	if res.Error != nil {
		return 0, res.Error
	}
	var row ShareLink
	if err := r.DB.WithContext(ctx).Select("view_count").Where("id = ?", id).First(&row).Error; err != nil {
		return 0, err
	}
	return row.ViewCount, nil
}

func (r *Repository) SetStatus(ctx context.Context, id int64, status int) error {
	return r.DB.WithContext(ctx).Model(&ShareLink{}).Where("id = ?", id).Update("status", status).Error
}

func (r *Repository) SoftDelete(ctx context.Context, id int64) error {
	return r.DB.WithContext(ctx).Delete(&ShareLink{}, id).Error
}

type ListFilter struct {
	OwnerUserID    int
	IncludeExpired bool
	Page           int
	Size           int
	Now            int64
}

func (r *Repository) ListByOwner(ctx context.Context, f ListFilter) ([]ShareLink, int64, error) {
	if f.Size <= 0 || f.Size > 200 {
		f.Size = 50
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	q := r.DB.WithContext(ctx).Model(&ShareLink{}).Where("owner_user_id = ?", f.OwnerUserID)
	if !f.IncludeExpired {
		q = q.Where("status = ?", 1).Where("expires_at IS NULL OR expires_at > FROM_UNIXTIME(?)", f.Now)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []ShareLink
	err := q.Order("id desc").Offset((f.Page - 1) * f.Size).Limit(f.Size).Find(&rows).Error
	return rows, total, err
}

// SumUserBytes returns total active bytes for a user — used to enforce
// per-user quota at upload time.
func (r *Repository) SumUserBytes(ctx context.Context, userID int) (int64, error) {
	var total int64
	err := r.DB.WithContext(ctx).
		Model(&ShareLink{}).
		Where("owner_user_id = ? AND status = ?", userID, 1).
		Select("COALESCE(SUM(size_bytes), 0)").
		Row().Scan(&total)
	return total, err
}
