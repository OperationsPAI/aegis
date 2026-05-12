package sso

import (
	"fmt"
	"time"

	"aegis/platform/consts"
	"aegis/platform/model"
)

// UserInfoResp is the SSO-facing user projection. Password and APIKeys are
// never included.
type UserInfoResp struct {
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

func NewUserInfoResp(u *model.User) *UserInfoResp {
	return &UserInfoResp{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		FullName:    u.FullName,
		Avatar:      u.Avatar,
		IsActive:    u.IsActive,
		Status:      int(u.Status),
		CreatedAt:   u.CreatedAt,
		LastLoginAt: u.LastLoginAt,
	}
}

// BatchUsersReq carries the ids whose UserInfoResp the caller wants.
type BatchUsersReq struct {
	IDs []int `json:"ids" binding:"required"`
}

func (r *BatchUsersReq) Validate() error {
	if len(r.IDs) == 0 {
		return fmt.Errorf("ids cannot be empty")
	}
	if len(r.IDs) > 1000 {
		return fmt.Errorf("too many ids (max 1000)")
	}
	return nil
}

// ListUsersReq paginates over users.
type ListUsersReq struct {
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
	IsActive *bool              `json:"is_active,omitempty"`
	Status   *consts.StatusType `json:"status,omitempty"`
}

type ListUsersResp struct {
	Users    []*UserInfoResp `json:"users"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

// CheckReq is the body of POST /v1/check.
type CheckReq struct {
	UserID     int    `json:"user_id" binding:"required"`
	Permission string `json:"permission" binding:"required"`
	ScopeType  string `json:"scope_type,omitempty"`
	ScopeID    string `json:"scope_id,omitempty"`
}

func (r *CheckReq) Validate() error {
	if r.UserID <= 0 {
		return fmt.Errorf("user_id must be positive")
	}
	if r.Permission == "" {
		return fmt.Errorf("permission cannot be empty")
	}
	if r.ScopeType != "" && r.ScopeID == "" {
		return fmt.Errorf("scope_id required when scope_type is set")
	}
	return nil
}

type CheckResp struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

// BatchCheckReq carries multiple CheckReq.
type BatchCheckReq struct {
	Checks []CheckReq `json:"checks" binding:"required"`
}

// PermissionSpec is one entry of /v1/permissions:register.
type PermissionSpec struct {
	Name        string `json:"name" binding:"required"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	ScopeType   string `json:"scope_type"`
}

// RegisterPermissionsReq registers a batch of permissions for a service.
type RegisterPermissionsReq struct {
	Service     string           `json:"service" binding:"required"`
	Permissions []PermissionSpec `json:"permissions" binding:"required"`
}

func (r *RegisterPermissionsReq) Validate() error {
	if r.Service == "" {
		return fmt.Errorf("service cannot be empty")
	}
	if len(r.Permissions) == 0 {
		return fmt.Errorf("permissions cannot be empty")
	}
	seen := make(map[string]struct{}, len(r.Permissions))
	for i := range r.Permissions {
		p := &r.Permissions[i]
		if p.Name == "" {
			return fmt.Errorf("permission[%d].name is required", i)
		}
		if _, dup := seen[p.Name]; dup {
			return fmt.Errorf("permission name %q duplicated in request", p.Name)
		}
		seen[p.Name] = struct{}{}
	}
	return nil
}

type RegisterPermissionsResp struct {
	Registered int `json:"registered"`
	Updated    int `json:"updated"`
}

// GrantReq is shared by POST and DELETE /v1/grants.
type GrantReq struct {
	UserID    int    `json:"user_id" binding:"required"`
	RoleID    int    `json:"role_id,omitempty"`
	Role      string `json:"role,omitempty"`
	ScopeType string `json:"scope_type" binding:"required"`
	ScopeID   string `json:"scope_id" binding:"required"`
}

func (r *GrantReq) Validate() error {
	if r.UserID <= 0 {
		return fmt.Errorf("user_id must be positive")
	}
	if r.RoleID <= 0 && r.Role == "" {
		return fmt.Errorf("role or role_id required")
	}
	if r.ScopeType == "" || r.ScopeID == "" {
		return fmt.Errorf("scope_type and scope_id required")
	}
	return nil
}

type GrantResp struct {
	Granted bool `json:"granted"`
}

type RevokeResp struct {
	Revoked bool `json:"revoked"`
}

// UserGrantResp lists a user's scoped role assignments.
type UserGrantResp struct {
	Role      string    `json:"role"`
	ScopeType string    `json:"scope_type"`
	ScopeID   string    `json:"scope_id"`
	GrantedAt time.Time `json:"granted_at"`
}

// ScopeUserResp lists members of a scope.
type ScopeUserResp struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}
