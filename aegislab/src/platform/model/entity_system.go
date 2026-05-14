package model

import "time"

// =====================================================================
// System Registration Entities
// =====================================================================

// The `System` aggregate was retired in issue #75; the per-system runtime
// parameters (count / ns_pattern / status / …) now live in etcd (seeded via
// dynamic_configs). SystemMetadata stays in MySQL.

// SystemMetadata stores per-system metadata (service endpoints, java methods, etc.)
type SystemMetadata struct {
	ID           int       `gorm:"primaryKey;autoIncrement"`
	SystemName   string    `gorm:"not null;size:64;uniqueIndex:idx_system_meta,priority:1"`
	MetadataType string    `gorm:"not null;size:64;uniqueIndex:idx_system_meta,priority:2"`
	ServiceName  string    `gorm:"not null;size:256;uniqueIndex:idx_system_meta,priority:3"`
	Data         string    `gorm:"type:json;not null"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime"`
}
