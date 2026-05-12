package blob

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Repository is the metadata-table access surface. The DB stores
// metadata only; bytes live in the driver.
type Repository struct {
	DB *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{DB: db} }

// Create inserts a new ObjectRecord. Called when a presign is issued
// (size_bytes=0, etag empty) and again — via Update — when the upload
// completion callback fires.
func (r *Repository) Create(ctx context.Context, rec *ObjectRecord) error {
	return r.DB.WithContext(ctx).Create(rec).Error
}

func (r *Repository) FindByKey(ctx context.Context, bucket, key string) (*ObjectRecord, error) {
	var rec ObjectRecord
	err := r.DB.WithContext(ctx).
		Where("bucket = ? AND storage_key = ? AND deleted_at IS NULL", bucket, key).
		First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrObjectNotFound
	}
	return &rec, err
}

// MarkUploaded patches in the actual size + etag from the driver after
// the frontend confirms the upload (or the reconcile job spots an
// orphan).
func (r *Repository) MarkUploaded(ctx context.Context, id int64, size int64, etag string) error {
	return r.DB.WithContext(ctx).
		Model(&ObjectRecord{}).
		Where("id = ?", id).
		Updates(map[string]any{"size_bytes": size, "etag": etag}).Error
}

// SoftDelete sets deleted_at; the lifecycle worker drives the
// driver-side delete asynchronously.
func (r *Repository) SoftDelete(ctx context.Context, bucket, key string) error {
	now := time.Now()
	res := r.DB.WithContext(ctx).
		Model(&ObjectRecord{}).
		Where("bucket = ? AND storage_key = ? AND deleted_at IS NULL", bucket, key).
		Update("deleted_at", now)
	if res.RowsAffected == 0 && res.Error == nil {
		return ErrObjectNotFound
	}
	return res.Error
}

// ListByEntity is the inverse-index lookup ("show me everything dataset
// 42 owns").
type ListFilter struct {
	Bucket     string
	EntityKind string
	EntityID   string
	UploadedBy *int
	Cursor     int64
	Limit      int
}

func (r *Repository) List(ctx context.Context, f ListFilter) ([]ObjectRecord, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	q := r.DB.WithContext(ctx).Where("deleted_at IS NULL")
	if f.Bucket != "" {
		q = q.Where("bucket = ?", f.Bucket)
	}
	if f.EntityKind != "" {
		q = q.Where("entity_kind = ?", f.EntityKind)
	}
	if f.EntityID != "" {
		q = q.Where("entity_id = ?", f.EntityID)
	}
	if f.UploadedBy != nil {
		q = q.Where("uploaded_by = ?", *f.UploadedBy)
	}
	if f.Cursor > 0 {
		q = q.Where("id < ?", f.Cursor)
	}
	var rows []ObjectRecord
	if err := q.Order("id desc").Limit(f.Limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
