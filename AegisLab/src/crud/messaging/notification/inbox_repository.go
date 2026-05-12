package notification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// InboxRepository is the data-access surface for the per-user inbox
// rows. Read paths are optimised for the bell (unread count) and the
// inbox page (paginated list with filters); write paths are
// idempotent so producer retries don't double-post.
type InboxRepository struct {
	DB *gorm.DB
}

func NewInboxRepository(db *gorm.DB) *InboxRepository { return &InboxRepository{DB: db} }

const dedupeWindow = 10 * time.Minute

// FindByDedupeKey returns an existing notification for the same user
// + dedupe key within the dedupe window, if any. Empty dedupe key
// disables dedupe.
func (r *InboxRepository) FindByDedupeKey(ctx context.Context, userID int, key string, since time.Time) (*Notification, error) {
	if key == "" {
		return nil, nil
	}
	var n Notification
	err := r.DB.WithContext(ctx).
		Where("user_id = ? AND dedupe_key = ? AND created_at >= ?", userID, key, since).
		Order("created_at desc").
		First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &n, err
}

func (r *InboxRepository) Create(ctx context.Context, n *Notification) error {
	return r.DB.WithContext(ctx).Create(n).Error
}

// ListFilter captures the query knobs the inbox endpoint accepts.
// Cursor is the id of the last item from the previous page (descending
// by id, so "next page" is `id < cursor`).
type ListFilter struct {
	UserID     int
	UnreadOnly bool
	Category   string
	Severity   string
	Cursor     int64
	Limit      int
}

func (r *InboxRepository) List(ctx context.Context, f ListFilter) ([]Notification, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	q := r.DB.WithContext(ctx).
		Where("user_id = ? AND archived_at IS NULL", f.UserID)
	if f.UnreadOnly {
		q = q.Where("read_at IS NULL")
	}
	if f.Category != "" {
		q = q.Where("category = ?", f.Category)
	}
	if f.Severity != "" {
		q = q.Where("severity = ?", f.Severity)
	}
	if f.Cursor > 0 {
		q = q.Where("id < ?", f.Cursor)
	}
	var rows []Notification
	if err := q.Order("id desc").Limit(f.Limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *InboxRepository) Get(ctx context.Context, userID int, id int64) (*Notification, error) {
	var n Notification
	err := r.DB.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("notification not found")
	}
	return &n, err
}

func (r *InboxRepository) MarkRead(ctx context.Context, userID int, id int64) error {
	now := time.Now()
	res := r.DB.WithContext(ctx).
		Model(&Notification{}).
		Where("id = ? AND user_id = ? AND read_at IS NULL", id, userID).
		Update("read_at", now)
	return res.Error
}

func (r *InboxRepository) MarkAllRead(ctx context.Context, userID int, category string) (int64, error) {
	now := time.Now()
	q := r.DB.WithContext(ctx).
		Model(&Notification{}).
		Where("user_id = ? AND read_at IS NULL AND archived_at IS NULL", userID)
	if category != "" {
		q = q.Where("category = ?", category)
	}
	res := q.Update("read_at", now)
	return res.RowsAffected, res.Error
}

func (r *InboxRepository) Archive(ctx context.Context, userID int, id int64) error {
	now := time.Now()
	return r.DB.WithContext(ctx).
		Model(&Notification{}).
		Where("id = ? AND user_id = ? AND archived_at IS NULL", id, userID).
		Update("archived_at", now).Error
}

func (r *InboxRepository) UnreadCount(ctx context.Context, userID int) (int64, error) {
	var n int64
	err := r.DB.WithContext(ctx).
		Model(&Notification{}).
		Where("user_id = ? AND read_at IS NULL AND archived_at IS NULL", userID).
		Count(&n).Error
	return n, err
}
