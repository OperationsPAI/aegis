package model

import (
	"time"

	"aegis/platform/consts"
)

// OIDCClient registers an OIDC relying-party that can obtain tokens from the
// SSO. Service is the owner — Task #13 uses it for delegated admin filtering.
type OIDCClient struct {
	ID               int               `gorm:"primaryKey;autoIncrement"`
	ClientID         string            `gorm:"not null;size:64;uniqueIndex"`
	ClientSecretHash string            `gorm:"not null;size:255"`
	Name             string            `gorm:"not null;size:128"`
	Service          string            `gorm:"not null;size:64;index"`
	RedirectURIs     []string          `gorm:"type:json;serializer:json"`
	Grants           []string          `gorm:"type:json;serializer:json"`
	Scopes           []string          `gorm:"type:json;serializer:json"`
	IsConfidential   bool              `gorm:"not null;default:true"`
	Status           consts.StatusType `gorm:"not null;default:1;index"`
	CreatedAt        time.Time         `gorm:"autoCreateTime"`
	UpdatedAt        time.Time         `gorm:"autoUpdateTime"`
}

// GORM's snake_case namer turns OIDCClient into "o_id_c_clients" because
// it treats each capital letter as a word boundary. Pin the table name.
func (OIDCClient) TableName() string { return "oidc_clients" }
