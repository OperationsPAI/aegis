package sso

import "time"

type IdentityProvider struct {
	ID            int       `json:"id" gorm:"primaryKey;autoIncrement"`
	Name          string    `json:"name" gorm:"uniqueIndex;size:32;not null"`
	DisplayName   string    `json:"display_name" gorm:"size:128"`
	Type          string    `json:"type" gorm:"size:16;not null"`
	ClientID      string    `json:"client_id" gorm:"size:255;not null"`
	ClientSecret  string    `json:"-" gorm:"size:255;not null"`
	DiscoveryURL  string    `json:"discovery_url,omitempty" gorm:"size:512"`
	AuthorizeURL  string    `json:"authorize_url,omitempty" gorm:"size:512"`
	TokenURL      string    `json:"token_url,omitempty" gorm:"size:512"`
	UserinfoURL   string    `json:"userinfo_url,omitempty" gorm:"size:512"`
	Scopes        string    `json:"scopes" gorm:"size:512;not null"`
	ClaimMapping  string    `json:"claim_mapping,omitempty" gorm:"type:text"`
	AutoProvision bool      `json:"auto_provision" gorm:"default:true"`
	DefaultRoles  string    `json:"default_roles,omitempty" gorm:"size:512"`
	Enabled       bool      `json:"enabled" gorm:"default:true"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type UserIdentity struct {
	ID            int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	UserID        int        `json:"user_id" gorm:"index;not null"`
	Provider      string     `json:"provider" gorm:"size:32;not null;uniqueIndex:idx_provider_sub"`
	ExternalSub   string     `json:"external_sub" gorm:"size:255;not null;uniqueIndex:idx_provider_sub"`
	ExternalEmail string     `json:"external_email,omitempty" gorm:"size:255"`
	Metadata      string     `json:"metadata,omitempty" gorm:"type:text"`
	LinkedAt      time.Time  `json:"linked_at" gorm:"not null"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
}
