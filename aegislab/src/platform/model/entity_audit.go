package model

import (
	"time"

	"aegis/platform/consts"
)

// AuditLog represents an audit log entry
type AuditLog struct {
	ID                 int    `gorm:"primaryKey;autoIncrement" json:"id"`
	IPAddress          string `gorm:"not null;default:'127.0.0.1'" json:"ip_address"`      // IP address of the client
	UserAgent          string `gorm:"not null;type:text" json:"user_agent"`                // User agent of the client
	Duration           int    `json:"duration"`                                            // Duration in milliseconds
	Action             string `gorm:"not null;index:idx_audit_action_state" json:"action"` // Action performed (CREATE, UPDATE, DELETE, etc.)
	Details            string `gorm:"type:text" json:"details"`                            // Additional details in JSON format
	ErrorMsg           string `gorm:"type:text" json:"error_msg,omitempty"`                // Error message if state is FAILED
	UserID             int    `gorm:"not null;index:idx_audit_user_time" json:"user_id"`   // User who performed the action (nullable for system actions)
	ResourceID         int    `gorm:"not null;index" json:"resource_id"`                   // ID of the affected resource
	ResourceInstanceID *int   `json:"resource_instance_id,omitempty"`                      // Actual business object ID (e.g., dataset_id=5, container_id=10)
	ResourceInstance   string `gorm:"type:varchar(128)" json:"resource_instance"`          // Composite identifier, e.g., "datasets:5", "containers:10"

	State     consts.AuditLogState `gorm:"not null;default:0;index:idx_audit_action_state" json:"state"` // SUCCESS, FAILED, WARNING
	Status    consts.StatusType    `gorm:"not null;default:1;index" json:"status"`                       // Status: -1:deleted 0:disabled 1:enabled
	CreatedAt time.Time            `gorm:"autoCreateTime;index:idx_audit_user_time" json:"created_at"`   // When the action was performed

	// Foreign key association. The User association used to live here; aegislab
	// now resolves audit-log user info via ssoclient (the SSO process owns
	// users). The DB-level FK can stay since both tables share the same MySQL
	// instance in PR-1.
	Resource *Resource `gorm:"foreignKey:ResourceID"`
}
