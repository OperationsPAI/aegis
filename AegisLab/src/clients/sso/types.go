package ssoclient

import "time"

type CheckParams struct {
	UserID     int
	Permission string
	ScopeType  string
	ScopeID    string
}

type GrantParams struct {
	UserID    int
	Role      string
	ScopeType string
	ScopeID   string
}

type PermissionSpec struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	ScopeType   string `json:"scope_type,omitempty"`
}

type UserInfo struct {
	ID          int        `json:"id"`
	Username    string     `json:"username"`
	Email       string     `json:"email"`
	FullName    string     `json:"full_name"`
	Avatar      string     `json:"avatar,omitempty"`
	IsActive    bool       `json:"is_active"`
	Status      int        `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}
