package share

import (
	"time"

	"gorm.io/gorm"
)

type ShareLink struct {
	ID               int64          `gorm:"primaryKey;autoIncrement" json:"-"`
	ShortCode        string         `gorm:"uniqueIndex;size:12;not null" json:"short_code"`
	Bucket           string         `gorm:"size:64;not null" json:"bucket"`
	ObjectKey        string         `gorm:"size:255;not null" json:"object_key"`
	OwnerUserID      int            `gorm:"index;not null" json:"owner_user_id"`
	OriginalFilename string         `gorm:"size:255" json:"original_filename"`
	ContentType      string         `gorm:"size:127" json:"content_type"`
	SizeBytes        int64          `gorm:"not null;default:0" json:"size_bytes"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	ExpiresAt        *time.Time     `json:"expires_at,omitempty"`
	MaxViews         *int           `json:"max_views,omitempty"`
	ViewCount        int            `gorm:"not null;default:0" json:"view_count"`
	Status           int            `gorm:"not null;default:1" json:"status"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"-"`
}

func (ShareLink) TableName() string { return "share_links" }
