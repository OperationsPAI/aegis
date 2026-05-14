package model

import (
	"fmt"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dockerutil"
	"aegis/platform/utils"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"gorm.io/gorm"
)

type Container struct {
	ID     int                  `gorm:"primaryKey;autoIncrement"`
	Name   string               `gorm:"not null;size:128"`
	Type   consts.ContainerType `gorm:"not null;size:64"`
	README string               `gorm:"type:mediumtext"`

	IsPublic  bool              `gorm:"not null;default:false"`
	Status    consts.StatusType `gorm:"not null;default:1;index:idx_container_status_type"`
	CreatedAt time.Time         `gorm:"autoCreateTime"`
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`

	ActiveName string `gorm:"type:varchar(150) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN name ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_container_name"`

	// Many-to-many relationship with labels
	Versions []ContainerVersion `gorm:"foreignKey:ContainerID"`
	Labels   []Label            `gorm:"many2many:container_labels"`
}

func (c *Container) BeforeCreate(tx *gorm.DB) error {
	if c.Type == consts.ContainerTypePedestal {
		system := chaos.SystemType(c.Name)
		if !system.IsValid() {
			return fmt.Errorf("invalid pedestal name: %s", c.Name)
		}
	}
	return nil
}

type ContainerVersion struct {
	ID        int    `gorm:"primaryKey;autoIncrement"`
	Name      string `gorm:"not null;size:32;default:'1.0.0'"`
	NameMajor int    `gorm:"index:idx_container_version_name_order"`
	NameMinor int    `gorm:"index:idx_container_version_name_order"`
	NamePatch int    `gorm:"index:idx_container_version_name_order"`

	GithubLink  string `gorm:"size:512"`
	Registry    string `gorm:"not null;default:'docker.io';size:64"`
	Namespace   string `gorm:"size:128"`
	Repository  string `gorm:"not null;size:128"`
	Tag         string `gorm:"not null;size:128"`
	Command     string `gorm:"type:text"`
	Usage       int    `gorm:"column:usage_count;check:usage_count >= 0;default:0"`
	ContainerID int    `gorm:"not null;index:idx_cv_container_status"`
	UserID      int    `gorm:"not null;index"`

	Status    consts.StatusType `gorm:"not null;default:1;index:idx_cv_container_status"`
	CreatedAt time.Time         `gorm:"autoCreateTime"`
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`

	ActiveVersionKey string `gorm:"type:varchar(40) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN CONCAT(container_id, ':', name) ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_version_unique"`

	ImageRef string `gorm:"-"`

	// Foreign key association
	Container *Container `gorm:"foreignKey:ContainerID"`

	// One-to-one relationship with HelmConfig
	HelmConfig *HelmConfig `gorm:"foreignKey:ContainerVersionID;references:ID"`

	// Many-to-many relationship with ParameterConfig (for environment variables)
	EnvVars []ParameterConfig `gorm:"many2many:container_version_env_vars"`
}

func (cv *ContainerVersion) BeforeCreate(tx *gorm.DB) error {
	if cv.Name != "" {
		major, minor, patch, err := utils.ParseSemanticVersion(cv.Name)
		if err != nil {
			return fmt.Errorf("invalid semantic version: %w", err)
		}

		cv.NameMajor = major
		cv.NameMinor = minor
		cv.NamePatch = patch
	}

	if cv.ImageRef != "" {
		registry, namespace, repository, tag, err := dockerutil.ParseFullImageRefernce(cv.ImageRef)
		if err != nil {
			return fmt.Errorf("invalid image reference: %w", err)
		}

		cv.Registry = registry
		cv.Namespace = namespace
		cv.Repository = repository
		cv.Tag = tag
	}
	return nil
}

// AfterFind GORM hook - set the Image field after retrieving from DB.
// Pedestal (helm) versions have no image; skip composition when both
// Repository and Tag are empty so we don't surface "docker.io/:" strings.
func (c *ContainerVersion) AfterFind(tx *gorm.DB) error {
	if c.Repository == "" && c.Tag == "" {
		c.ImageRef = ""
		return nil
	}
	if c.Namespace == "" {
		c.ImageRef = fmt.Sprintf("%s/%s:%s", c.Registry, c.Repository, c.Tag)
	} else {
		c.ImageRef = fmt.Sprintf("%s/%s/%s:%s", c.Registry, c.Namespace, c.Repository, c.Tag)
	}
	return nil
}

type HelmConfig struct {
	ID                 int    `gorm:"primaryKey;autoIncrement"` // Unique identifier
	ChartName          string `gorm:"not null;size:128"`        // Helm chart name
	Version            string `gorm:"not null;size:32"`         // Helm chart version (semantic version)
	ContainerVersionID int    `gorm:"not null;uniqueIndex"`     // Associated ContainerVersion ID (one-to-one relationship)

	// Remote source fields (required)
	RepoURL  string `gorm:"not null;size:512"` // Repository URL (required)
	RepoName string `gorm:"not null;size:128"` // Repository name (required)

	// Local source fields (optional fallback when remote fails)
	LocalPath string `gorm:"size:512"` // Local chart path (e.g., PVC path or object storage URL), optional fallback
	Checksum  string `gorm:"size:64"`  // Chart package SHA256 checksum (optional, for integrity verification)

	// Other fields
	ValueFile string `gorm:"size:512"` // Values file path

	// Foreign key association
	ContainerVersion *ContainerVersion `gorm:"foreignKey:ContainerVersionID;constraint:OnDelete:CASCADE"`

	// Many-to-many relationship with ParameterConfig (for Helm values)
	DynamicValues []ParameterConfig `gorm:"many2many:helm_config_values"`
}

func (h *HelmConfig) BeforeCreate(tx *gorm.DB) error {
	if h.Version == "" {
		return fmt.Errorf("chart version is required")
	}
	if h.RepoURL == "" {
		return fmt.Errorf("RepoURL is required")
	}
	if h.RepoName == "" {
		return fmt.Errorf("RepoName is required")
	}
	return nil
}

// ParameterConfig is a (system_id, config_key, type, category)-scoped row.
// SystemID is the owning containers.id; nullable rows are cluster-wide
// (no single owning system, e.g. legitimately shared cross-system params).
// The unique index uses (system_id, config_key, type, category) so two
// systems can declare the same chart value path with different defaults
// — see issue #314 for the DSB-family hs/sn/media collision that motivated
// scoping the index.
type ParameterConfig struct {
	ID       int  `gorm:"primaryKey;autoIncrement"`
	SystemID *int `gorm:"column:system_id;index"`
	// SystemIDKey shadows SystemID with COALESCE(system_id, 0) so the unique
	// index on (system_id_key, config_key, type, category) actually enforces
	// uniqueness for cluster-wide rows. MySQL/SQLite/Postgres treat NULLs as
	// distinct in unique indexes, which would otherwise let two cluster-wide
	// rows share the same (config_key, type, category) tuple. Maintained by
	// BeforeSave below.
	SystemIDKey    int                      `gorm:"column:system_id_key;not null;default:0;uniqueIndex:idx_unique_config"`
	Key            string                   `gorm:"column:config_key;not null;size:128;uniqueIndex:idx_unique_config"`
	Type           consts.ParameterType     `gorm:"not null;default:0;uniqueIndex:idx_unique_config"`
	Category       consts.ParameterCategory `gorm:"not null;uniqueIndex:idx_unique_config"`
	ValueType      consts.ValueDataType     `gorm:"not null;default:0"`
	Description    string                   `gorm:"type:text"`
	DefaultValue   *string                  `gorm:"type:text"`
	TemplateString *string                  `gorm:"type:text"`
	Required       bool                     `gorm:"not null;default:false"`
	Overridable    bool                     `gorm:"not null;default:true"`
}

func (p *ParameterConfig) BeforeSave(tx *gorm.DB) error {
	if p.SystemID == nil {
		p.SystemIDKey = 0
	} else {
		p.SystemIDKey = *p.SystemID
	}
	return nil
}

func (p *ParameterConfig) BeforeCreate(tx *gorm.DB) error {
	switch p.Type {
	case consts.ParameterTypeFixed:
		if p.Required && p.DefaultValue == nil {
			return fmt.Errorf("default value is required for fixed parameters")
		}
	case consts.ParameterTypeDynamic:
		if p.TemplateString == nil || *p.TemplateString == "" {
			return fmt.Errorf("template string is required for dynamic parameters")
		}
	}

	switch p.Category {
	case consts.ParameterCategoryEnvVars:
		if err := utils.IsValidEnvVar(p.Key); err != nil {
			return fmt.Errorf("invalid environment variable key: %w", err)
		}
	case consts.ParameterCategoryHelmValues:
		if err := utils.IsValidHelmValueKey(p.Key); err != nil {
			return fmt.Errorf("invalid helm value key: %w", err)
		}
	}
	return nil
}

// ContainerVersionEnvVar Many-to-many relationship table between ContainerVersion and ParameterConfig (for environment variables)
type ContainerVersionEnvVar struct {
	ContainerVersionID int       `gorm:"primaryKey"`     // ContainerVersion ID
	ParameterConfigID  int       `gorm:"primaryKey"`     // ParameterConfig ID
	CreatedAt          time.Time `gorm:"autoCreateTime"` // Removed index - many-to-many tables rarely queried by creation time

	// Foreign key association
	ContainerVersion *ContainerVersion `gorm:"foreignKey:ContainerVersionID"`
	ParameterConfig  *ParameterConfig  `gorm:"foreignKey:ParameterConfigID"`
}

// HelmConfigValue Many-to-many relationship table between HelmConfig and ParameterConfig (for Helm values)
type HelmConfigValue struct {
	HelmConfigID      int       `gorm:"primaryKey"`     // HelmConfig ID
	ParameterConfigID int       `gorm:"primaryKey"`     // ParameterConfig ID
	CreatedAt         time.Time `gorm:"autoCreateTime"` // Removed index - many-to-many tables rarely queried by creation time

	// Foreign key association
	HelmConfig      *HelmConfig      `gorm:"foreignKey:HelmConfigID"`
	ParameterConfig *ParameterConfig `gorm:"foreignKey:ParameterConfigID"`
}

// ContainerLabel Many-to-many relationship table between Container and Label
type ContainerLabel struct {
	ContainerID int       `gorm:"primaryKey"`     // Container ID
	LabelID     int       `gorm:"primaryKey"`     // Label ID
	CreatedAt   time.Time `gorm:"autoCreateTime"` // Creation time

	// Foreign key association
	Container *Container `gorm:"foreignKey:ContainerID"`
	Label     *Label     `gorm:"foreignKey:LabelID"`
}
