package model

import (
	"time"

	"aegis/platform/consts"
)

// =====================================================================
// Dynamic Configuration Entities
// =====================================================================

// DynamicConfig stores configuration items that can be dynamically modified at runtime
type DynamicConfig struct {
	ID       int                `gorm:"primaryKey;autoIncrement"`                                          // Unique identifier
	Key      string             `gorm:"column:config_key;not null;index:idx_config_key_category;size:255"` // Configuration key (e.g., "debugging.enabled", "rate_limiting.max_concurrent_builds")
	Scope    consts.ConfigScope `gorm:"not null;default:0"`                                                // Configuration scope: producer, consumer
	Category string             `gorm:"not null;size:64;index:idx_config_key_category;default:'app'"`      // Configuration category: app, system, monitor, rate_limiting, database, k8s, etc.

	ValueType    consts.ConfigValueType `gorm:"not null;default:0"`     // Value type: string, bool, int, float64, []string
	Description  string                 `gorm:"type:text"`              // Human-readable description of what this configuration does
	IsSecret     bool                   `gorm:"not null;default:false"` // Whether this is sensitive data (passwords, tokens, etc.)
	DefaultValue string                 `gorm:"not null;type:text"`     // Default value for this configuration
	MinValue     *float64               `gorm:"type:decimal(20,4)"`     // Minimum value (for numeric types)
	MaxValue     *float64               `gorm:"type:decimal(20,4)"`     // Maximum value (for numeric types)
	Pattern      string                 `gorm:"size:512"`               // Regex pattern for string validation
	Options      string                 `gorm:"type:text"`              // JSON array of allowed values (for enum-like configs)

	UpdatedBy *int      // User ID who last updated this config
	CreatedAt time.Time `gorm:"autoCreateTime"` // Creation time
	UpdatedAt time.Time `gorm:"autoUpdateTime"` // Last update time

	// One-to-many relationship with ConfigHistory
	History []ConfigHistory `gorm:"foreignKey:ConfigID"`

	// Many-to-many relationship with labels
	Labels []Label `gorm:"many2many:config_labels"`
}

// ConfigHistory records all changes made to configuration items
type ConfigHistory struct {
	ID               int                             `gorm:"primaryKey;autoIncrement"` // Unique identifier
	ChangeType       consts.ConfigHistoryChangeType  `gorm:"not null;default:0"`       // Type of change: update, create, delete, rollback
	ChangeField      consts.ConfigHistoryChangeField `gorm:"not null;default:0"`       // Specific field that was changed (if applicable)
	OldValue         string                          `gorm:"not null;type:text"`       // Previous value
	NewValue         string                          `gorm:"not null;type:text"`       // New value after change
	Reason           string                          `gorm:"type:text"`                // Reason for this change (provided by operator)
	ConfigID         int                             `gorm:"not null;index"`           // Foreign key to DynamicConfig
	RolledBackFromID *int                            // If this is a rollback, references the history entry being rolled back from

	OperatorID *int   // User ID who made this change
	IPAddress  string `gorm:"size:64"`  // IP address from which the change was made
	UserAgent  string `gorm:"size:512"` // User agent of the client making the change

	CreatedAt time.Time `gorm:"autoCreateTime;not null"` // When this change was made

	// Foreign key associations
	Config         *DynamicConfig `gorm:"foreignKey:ConfigID"`
	RolledBackFrom *ConfigHistory `gorm:"foreignKey:RolledBackFromID"`
}

// ConfigLabel Many-to-many relationship table between DynamicConfig and Label
type ConfigLabel struct {
	ConfigID  int       `gorm:"primaryKey"`     // Config ID
	LabelID   int       `gorm:"primaryKey"`     // Label ID
	CreatedAt time.Time `gorm:"autoCreateTime"` // Creation time

	// Foreign key association
	Config *DynamicConfig `gorm:"foreignKey:ConfigID"`
	Label  *Label         `gorm:"foreignKey:LabelID"`
}
