package blob

import "time"

// ObjectRecord is the DB row for one stored object. The DB stores
// metadata only — bytes live in the driver. The lifecycle worker walks
// this table to delete expired objects; the list endpoint queries it
// by (entity_kind, entity_id) and by uploader.
type ObjectRecord struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	Bucket      string    `gorm:"not null;size:64;index:idx_bucket_key,priority:1"`
	StorageKey  string    `gorm:"not null;size:512;index:idx_bucket_key,priority:2"`
	SizeBytes   int64     `gorm:"not null;default:0"`
	ContentType string    `gorm:"size:128"`
	ETag        string    `gorm:"size:128"`
	UploadedBy  *int      `gorm:"index:idx_uploader,priority:1"`
	EntityKind  string    `gorm:"size:64;index:idx_entity,priority:1"`
	EntityID    string    `gorm:"size:128;index:idx_entity,priority:2"`
	Metadata    []byte    `gorm:"type:json;serializer:json"`
	CreatedAt   time.Time `gorm:"autoCreateTime;index:idx_uploader,priority:2,sort:desc"`
	ExpiresAt   *time.Time
	DeletedAt   *time.Time
}

func (ObjectRecord) TableName() string { return "blob_objects" }
