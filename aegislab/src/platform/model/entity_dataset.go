package model

import (
	"fmt"
	"time"

	"aegis/platform/consts"
	"aegis/platform/utils"

	"gorm.io/gorm"
)

type Dataset struct {
	ID          int    `gorm:"primaryKey;autoIncrement"` // Unique identifier
	Name        string `gorm:"not null;size:128"`        // Dataset name with size limit
	Type        string `gorm:"not null;size:64"`         // Dataset type (e.g., "microservice", "database", "network")
	Description string `gorm:"type:mediumtext"`          // Dataset description

	IsPublic  bool              `gorm:"not null;default:false"`   // Whether public
	Status    consts.StatusType `gorm:"not null;default:1;index"` // Status: -1:deleted 0:disaBled 1:enabled
	CreatedAt time.Time         `gorm:"autoCreateTime"`           // Creation time
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`           // Update time

	ActiveName string `gorm:"type:varchar(150) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN name ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_dataset_name"`

	// Many-to-many relationships - use explicit intermediate tables for better control
	Versions []DatasetVersion `gorm:"foreignKey:DatasetID"`
	Labels   []Label          `gorm:"many2many:dataset_labels"`
}

type DatasetVersion struct {
	ID        int    `gorm:"primaryKey;autoIncrement"`
	Name      string `gorm:"not null;size:32;default:'1.0.0'"`
	NameMajor int    `gorm:"index:idx_dataset_version_name_order"`
	NameMinor int    `gorm:"index:idx_dataset_version_name_order"`
	NamePatch int    `gorm:"index:idx_dataset_version_name_order"`

	Checksum  string `gorm:"type:varchar(64)"`                         // File checksum
	FileCount int    `gorm:"not null;default:0;check:file_count >= 0"` // File count with validation
	DatasetID int    `gorm:"not null;index:idx_dv_dataset_status"`     // Associated Dataset ID
	UserID    int    `gorm:"not null;index"`                           // Creator User ID

	Status    consts.StatusType `gorm:"not null;default:1;index:idx_dv_dataset_status"` // Status: -1:deleted 0:disabled 1:enabled
	CreatedAt time.Time         `gorm:"autoCreateTime"`                                 // Creation time
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`                                 // Update time

	ActiveVersionKey string `gorm:"type:varchar(40) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN CONCAT(dataset_id, ':', name) ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_version_unique"`

	// Foreign key association
	Dataset *Dataset `gorm:"foreignKey:DatasetID"`

	Datapacks []FaultInjection `gorm:"many2many:dataset_version_injections"`
}

func (dv *DatasetVersion) BeforeCreate(tx *gorm.DB) error {
	if dv.Name != "" {
		major, minor, patch, err := utils.ParseSemanticVersion(dv.Name)
		if err != nil {
			return fmt.Errorf("invalid semantic version: %w", err)
		}

		dv.NameMajor = major
		dv.NameMinor = minor
		dv.NamePatch = patch
	}
	return nil
}

// DatasetLabel Many-to-many relationship table between Dataset and Label
type DatasetLabel struct {
	DatasetID int       `gorm:"primaryKey"`     // Dataset ID
	LabelID   int       `gorm:"primaryKey"`     // Label ID
	CreatedAt time.Time `gorm:"autoCreateTime"` // Creation time

	// Foreign key association
	Dataset *Dataset `gorm:"foreignKey:DatasetID"`
	Label   *Label   `gorm:"foreignKey:LabelID"`
}

// DatasetVersionInjection Many-to-many relationship table between DatasetVersion and FaultInjection
type DatasetVersionInjection struct {
	DatasetVersionID int       `gorm:"primaryKey"`
	InjectionID      int       `gorm:"primaryKey"`
	CreatedAt        time.Time `gorm:"autoCreateTime"` // Creation time

	// Foreign key associations
	DatasetVersion *DatasetVersion `gorm:"foreignKey:DatasetVersionID"`
	Injection      *FaultInjection `gorm:"foreignKey:InjectionID"`
}
