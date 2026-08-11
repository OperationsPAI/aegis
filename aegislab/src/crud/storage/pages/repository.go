package pages

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ErrNotFound is the sentinel for "no row matched". Service translates this
// to a 404; handlers should not check gorm.ErrRecordNotFound directly.
var ErrNotFound = errors.New("page site not found")

// Repository owns the page_sites table. Pure CRUD — business rules live
// in Service.
type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, site *PageSite) error {
	return r.db.WithContext(ctx).Create(site).Error
}

func (r *Repository) Update(ctx context.Context, site *PageSite) error {
	return r.db.WithContext(ctx).Save(site).Error
}

func (r *Repository) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&PageSite{}, id).Error
}

func (r *Repository) FindByID(ctx context.Context, id int64) (*PageSite, error) {
	var site PageSite
	if err := r.db.WithContext(ctx).First(&site, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &site, nil
}

func (r *Repository) FindBySlug(ctx context.Context, slug string) (*PageSite, error) {
	var site PageSite
	if err := r.db.WithContext(ctx).Where("slug = ?", slug).First(&site).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &site, nil
}

// SlugExists is a lightweight existence check used by slug auto-suffixing.
func (r *Repository) SlugExists(ctx context.Context, slug string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&PageSite{}).Where("slug = ?", slug).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListByOwner returns the owner's sites, newest first.
func (r *Repository) ListByOwner(ctx context.Context, ownerID, limit, offset int) ([]PageSite, error) {
	var sites []PageSite
	q := r.db.WithContext(ctx).Where("owner_id = ?", ownerID).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Find(&sites).Error; err != nil {
		return nil, err
	}
	return sites, nil
}

// ListPublic returns only sites whose visibility is public_listed.
func (r *Repository) ListPublic(ctx context.Context, limit, offset int) ([]PageSite, error) {
	var sites []PageSite
	q := r.db.WithContext(ctx).Where("visibility = ?", VisibilityPublicListed).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Find(&sites).Error; err != nil {
		return nil, err
	}
	return sites, nil
}
