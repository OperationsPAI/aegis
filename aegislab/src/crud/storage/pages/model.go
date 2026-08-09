package pages

import (
	"regexp"
	"time"
)

// Visibility values stored on PageSite. Validated at the service edge —
// the column is varchar so GORM doesn't enforce it.
const (
	VisibilityPublicListed   = "public_listed"
	VisibilityPublicUnlisted = "public_unlisted"
	VisibilityPrivate        = "private"
)

// BucketName is the LOGICAL bucket name the blob Registry resolves to
// a physical S3 bucket via `blob.buckets.<name>.bucket`. Convention
// across modules (datapack, dataset, shared, …) is short logical →
// `aegis-<x>` physical; passing the physical name here would surface as
// `ErrBucketNotFound` on every PutBytes/GetBytes call.
const BucketName = "pages"

// Upload limits — kept as package vars so tests can override.
var (
	MaxFileSize  int64 = 10 * 1024 * 1024
	MaxTotalSize int64 = 50 * 1024 * 1024
	MaxFiles           = 200
)

// SlugRegex anchors a relaxed DNS-style slug — lowercase, digits, dashes,
// up to 63 chars total, starting with [a-z0-9].
var SlugRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// PageSite is the metadata row for a published static site. The actual
// files live in blob storage at {BucketName}/{SiteUUID}/{path}.
type PageSite struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"        json:"id"`
	SiteUUID   string    `gorm:"size:36;uniqueIndex;not null"    json:"site_uuid"`
	OwnerID    int       `gorm:"not null;index"                  json:"owner_id"`
	Slug       string    `gorm:"size:128;uniqueIndex;not null"   json:"slug"`
	Visibility string    `gorm:"size:32;not null"                json:"visibility"`
	Title      string    `gorm:"size:256"                        json:"title"`
	SizeBytes  int64     `                                       json:"size_bytes"`
	FileCount  int32     `                                       json:"file_count"`
	CreatedAt  time.Time `                                       json:"created_at"`
	UpdatedAt  time.Time `                                       json:"updated_at"`
}

func (PageSite) TableName() string { return "page_sites" }

// IsValidVisibility returns true if v is one of the three accepted values.
func IsValidVisibility(v string) bool {
	switch v {
	case VisibilityPublicListed, VisibilityPublicUnlisted, VisibilityPrivate:
		return true
	}
	return false
}
