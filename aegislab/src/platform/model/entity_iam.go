package model

import (
	"fmt"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"

	"gorm.io/gorm"
)

// User table
type User struct {
	ID          int        `gorm:"primaryKey;autoIncrement"` // Unique identifier
	Username    string     `gorm:"unique;not null;size:64"`  // Username (unique) with size limit
	Email       string     `gorm:"unique;not null;size:128"` // Email (unique) with size limit
	Password    string     `gorm:"not null;size:255"`        // Password (not returned to frontend) with size limit
	FullName    string     `gorm:"not null;size:128"`        // Full name with size limit
	Avatar      string     `gorm:"size:512"`                 // Avatar URL with size limit
	Phone       string     `gorm:"size:32"`                  // Phone number
	LastLoginAt *time.Time // Last login time

	IsActive  bool              `gorm:"not null;default:true"`    // Whether active
	Status    consts.StatusType `gorm:"not null;default:1;index"` // Status: -1:deleted 0:disabled 1:enabled
	CreatedAt time.Time         `gorm:"autoCreateTime"`           // Creation time
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`           // Update time

	ActiveUsername string `gorm:"type:varchar(64) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN username ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_username"`
}

// BeforeCreate GORM hook - hash the password before creating a new user
func (u *User) BeforeCreate(tx *gorm.DB) error {
	hashedPassword, err := crypto.HashPassword(u.Password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	u.Password = hashedPassword
	return nil
}

type APIKey struct {
	ID                  int      `gorm:"primaryKey;autoIncrement"`
	UserID              int      `gorm:"not null;index:idx_api_key_owner_status"`
	Name                string   `gorm:"not null;size:128"`
	Description         string   `gorm:"type:text"`
	KeyID               string   `gorm:"not null;size:64"`
	KeySecretHash       string   `gorm:"not null;size:255"`
	KeySecretCiphertext string   `gorm:"not null;type:text"`
	Scopes              []string `gorm:"type:json;serializer:json"`
	RevokedAt           *time.Time
	LastUsedAt          *time.Time
	ExpiresAt           *time.Time
	Status              consts.StatusType `gorm:"not null;default:1;index:idx_api_key_owner_status"`
	CreatedAt           time.Time         `gorm:"autoCreateTime"`
	UpdatedAt           time.Time         `gorm:"autoUpdateTime"`

	ActiveKeyID string `gorm:"type:varchar(64) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN key_id ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_api_key"`

	User *User `gorm:"foreignKey:UserID"`
}

// Role table
type Role struct {
	ID          int    `gorm:"primaryKey;autoIncrement"` // Unique identifier
	Name        string `gorm:"not null;size:32"`         // Role name (unique)
	DisplayName string `gorm:"not null"`                 // Display name
	Description string `gorm:"type:text"`                // Role description

	IsSystem  bool              `gorm:"not null;default:false"`   // Whether system role
	Status    consts.StatusType `gorm:"not null;default:1;index"` // 0:disabled 1:enabled -1:deleted
	CreatedAt time.Time         `gorm:"autoCreateTime"`           // Creation time
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`           // Update time

	ActiveName string `gorm:"type:varchar(32) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN name ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_role_name"`
}

// Permission table
type Permission struct {
	ID          int               `gorm:"primaryKey;autoIncrement"`                 // Unique identifier
	Name        string            `gorm:"not null;size:128"`                        // Permission name (unique)
	DisplayName string            `gorm:"not null"`                                 // Display name
	Description string            `gorm:"type:text"`                                // Permission description
	Action      consts.ActionName `gorm:"type:varchar(16);not null;index:idx_perm"` // Action (read, write, delete, execute, etc.)

	// Service identifies which downstream owns this permission (defaults to "aegis").
	// Lets multiple services register permissions without name collisions.
	Service string `gorm:"not null;size:64;default:'aegis';index"`
	// ScopeType is empty for global permissions; otherwise the UserScopedRole
	// scope_type the permission applies to (e.g. "aegis.project").
	ScopeType string `gorm:"size:64;index"`

	IsSystem  bool              `gorm:"not null;default:false"`   // Whether system permission
	Status    consts.StatusType `gorm:"not null;default:1;index"` // 0:disabled 1:enabled -1:deleted
	CreatedAt time.Time         `gorm:"autoCreateTime"`           // Creation time
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`           // Update time

	ActiveName string `gorm:"type:varchar(128) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN name ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_permission_name"`
}

// Resource table
type Resource struct {
	ID          int                     `gorm:"primaryKey;autoIncrement"`               // Unique identifier
	Name        consts.ResourceName     `gorm:"not null;uniqueIndex;size:64" `          // Resource name (unique)
	DisplayName string                  `gorm:"not null"`                               // Display name
	Description string                  `gorm:"type:text"`                              // Resource description
	Type        consts.ResourceType     `gorm:"not null"`                               // Resource type (table, api, function, etc.)
	Category    consts.ResourceCategory `gorm:"not null;index:idx_res_category_parent"` // Resource category
	ParentID    *int                    `gorm:"index:idx_res_category_parent"`          // Parent resource ID (supports hierarchy)

	CreatedAt time.Time `gorm:"autoCreateTime"` // Creation time

	// Foreign key association
	Parent *Resource `gorm:"foreignKey:ParentID"`
}

// UserScopedRole is the single table that replaces the four scope-specific
// join tables (user_projects / user_teams / user_containers / user_datasets).
// ScopeType identifies the kind of business object (e.g. "aegis.project") and
// ScopeID stores the business-side ID as a string so non-int IDs from other
// services can fit. The schema is owned by the SSO process.
type UserScopedRole struct {
	ID        int               `gorm:"primaryKey;autoIncrement"`
	UserID    int               `gorm:"not null;index:idx_usr_scope"`
	RoleID    int               `gorm:"not null;index:idx_usr_scope"`
	ScopeType string            `gorm:"not null;size:64;index:idx_usr_scope"`
	ScopeID   string            `gorm:"not null;size:64;index:idx_usr_scope"`
	Status    consts.StatusType `gorm:"not null;default:1;index"`
	CreatedAt time.Time         `gorm:"autoCreateTime"`
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`

	Active string `gorm:"->;type:varchar(160) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN CONCAT(user_id,':',role_id,':',scope_type,':',scope_id) ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_user_scoped_role"`

	Role *Role `gorm:"foreignKey:RoleID"`
}

// UserProjectWorkspace holds aegislab business data that previously rode on
// UserProject. It's separate from RBAC ownership (UserScopedRole) on purpose:
// workspace preferences live with aegislab, role grants live with SSO.
type UserProjectWorkspace struct {
	ID        int       `gorm:"primaryKey;autoIncrement"`
	UserID    int       `gorm:"not null;uniqueIndex:idx_user_project_workspace,priority:1"`
	ProjectID int       `gorm:"not null;uniqueIndex:idx_user_project_workspace,priority:2"`
	Config    string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

// UserRole Many-to-many relationship table between User and global roles
type UserRole struct {
	ID     int `gorm:"primaryKey;autoIncrement"`                   // Unique identifier
	UserID int `gorm:"not null;index:idx_user_role_unique,unique"` // User ID
	RoleID int `gorm:"not null;index:idx_user_role_unique,unique"` // Role ID

	CreatedAt time.Time `gorm:"autoCreateTime"` // Creation time
	UpdatedAt time.Time `gorm:"autoUpdateTime"` // Update time

	// Foreign key association
	User *User `gorm:"foreignKey:UserID"`
	Role *Role `gorm:"foreignKey:RoleID"`
}

// RolePermission Many-to-many relationship table between Role and Permission
type RolePermission struct {
	ID           int `gorm:"primaryKey;autoIncrement"`                        // Unique identifier
	RoleID       int `gorm:"not null;uniqueIndex:idx_role_permission_unique"` // Role ID
	PermissionID int `gorm:"not null;uniqueIndex:idx_role_permission_unique"` // Permission ID

	CreatedAt time.Time `gorm:"autoCreateTime"` // Creation time
	UpdatedAt time.Time `gorm:"autoUpdateTime"` // Update time

	// Foreign key association
	Role       *Role       `gorm:"foreignKey:RoleID"`
	Permission *Permission `gorm:"foreignKey:PermissionID"`
}

// UserPermission User direct permission table (supplements role permissions, supports special permission assignment)
type UserPermission struct {
	ID           int              `gorm:"primaryKey;autoIncrement"`                                                                                         // Unique identifier
	UserID       int              `gorm:"not null;uniqueIndex:idx_up_container_unique;uniqueIndex:idx_up_dataset_unique;uniqueIndex:idx_up_project_unique"` // User ID
	PermissionID int              `gorm:"not null;uniqueIndex:idx_up_container_unique;uniqueIndex:idx_up_dataset_unique;uniqueIndex:idx_up_project_unique"` // Permission ID
	GrantType    consts.GrantType `gorm:"default:0;size:16"`                                                                                                // Grant type: grant, deny
	ExpiresAt    *time.Time       // Expiration time
	ContainerID  *int             `gorm:"uniqueIndex:idx_up_container_unique"` // Container ID (container-level permission, empty means global or project-level permission)
	DatasetID    *int             `gorm:"uniqueIndex:idx_up_dataset_unique"`   // Dataset ID (dataset-level permission, empty means global or project-level permission)
	ProjectID    *int             `gorm:"uniqueIndex:idx_up_project_unique"`   // Project ID (project-level permission, empty means global permission)

	CreatedAt time.Time `gorm:"autoCreateTime"` // Creation time
	UpdatedAt time.Time `gorm:"autoUpdateTime"` // Update time

	// Foreign key association
	User       *User       `gorm:"foreignKey:UserID"`
	Permission *Permission `gorm:"foreignKey:PermissionID"`
	Container  *Container  `gorm:"foreignKey:ContainerID"`
	Dataset    *Dataset    `gorm:"foreignKey:DatasetID"`
	Project    *Project    `gorm:"foreignKey:ProjectID"`
}
