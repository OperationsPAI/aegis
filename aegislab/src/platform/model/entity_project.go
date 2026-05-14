package model

import (
	"time"

	"aegis/platform/consts"
)

type Project struct {
	ID          int    `gorm:"primaryKey"`
	Name        string `gorm:"unique,index;not null;size:128"` // Project name with size limit
	Description string `gorm:"type:text"`                      // Project description
	TeamID      *int   `gorm:"index:idx_project_team"`         // Associated team ID (optional, project can belong to team or user)

	IsPublic  bool              `gorm:"not null;default:false"`   // Whether publicly visible
	Status    consts.StatusType `gorm:"not null;default:1;index"` // Status: -1:deleted 0:disabled 1:enabled
	CreatedAt time.Time         `gorm:"autoCreateTime"`           // Creation time
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`           // Update time

	ActiveName string `gorm:"type:varchar(150) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN name ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_project_name"`

	// Foreign key association
	Team *Team `gorm:"foreignKey:TeamID"`

	Containers []Container `gorm:"many2many:project_containers"`
	Datasets   []Dataset   `gorm:"many2many:project_datasets"`
	Labels     []Label     `gorm:"many2many:project_labels"`
}

// Label table - Unified label management
type Label struct {
	ID          int                  `gorm:"primaryKey;autoIncrement"`                                                // Unique identifier
	Key         string               `gorm:"column:label_key;not null;type:varchar(64);index:idx_label_key_category"` // Label key
	Value       string               `gorm:"column:label_value;not null;type:varchar(64)"`                            // Label value
	Category    consts.LabelCategory `gorm:"index:idx_label_key_category"`                                            // Label category (dataset, fault_injection, algorithm, container, etc.)
	Description string               `gorm:"type:text"`                                                               // Label description
	Color       string               `gorm:"type:varchar(7);default:'#1890ff'"`                                       // Label color (hex format)
	Usage       int                  `gorm:"not null;column:usage_count;default:0;check:usage_count>=0"`              // Usage count

	IsSystem  bool              `gorm:"not null;default:false"`   // Whether system label
	Status    consts.StatusType `gorm:"not null;default:1;index"` // Status: -1:deleted 0:disabled 1:enabled
	CreatedAt time.Time         `gorm:"autoCreateTime"`           // Creation time
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`           // Update time

	ActiveKeyValue string `gorm:"type:varchar(100) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN CONCAT(label_key, ':', label_value) ELSE NULL END) VIRTUAL;uniqueIndex:idx_key_value_unique"`
}

// Team table - Team management
type Team struct {
	ID          int    `gorm:"primaryKey;autoIncrement"` // Unique identifier
	Name        string `gorm:"not null;size:128"`        // Team name with size limit
	Description string `gorm:"type:text"`                // Team description

	IsPublic  bool              `gorm:"not null;default:false"`   // Whether publicly visible
	Status    consts.StatusType `gorm:"not null;default:1;index"` // Status: -1:deleted 0:disabled 1:enabled
	CreatedAt time.Time         `gorm:"autoCreateTime"`           // Creation time
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`           // Update time

	ActiveName string `gorm:"type:varchar(150) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN name ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_team_name"`

	// One-to-many relationship with Project
	Projects []Project `gorm:"foreignKey:TeamID"`
}

// ProjectLabel Many-to-many relationship table between Project and Label
type ProjectLabel struct {
	ProjectID int       `gorm:"primaryKey"`     // Project ID
	LabelID   int       `gorm:"primaryKey"`     // Label ID
	CreatedAt time.Time `gorm:"autoCreateTime"` // Creation time

	// Foreign key association
	Project *Project `gorm:"foreignKey:ProjectID"`
	Label   *Label   `gorm:"foreignKey:LabelID"`
}
