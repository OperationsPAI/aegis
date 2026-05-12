package sso

import (
	"context"
	"errors"
	"fmt"

	"aegis/consts"
	"aegis/model"
	"aegis/module/rbac"
	"aegis/module/user"

	"gorm.io/gorm"
)

// AdminService wires the `/v1/*` admin REST endpoints to the existing user
// service and the rbac repository. Each method returns either domain DTOs or
// sentinel errors from `consts.Err*`; the handler layer translates those to
// HTTP status codes via `httpx.HandleServiceError`.
type AdminService struct {
	users *user.Service
	rbac  *rbac.Repository
}

func NewAdminService(users *user.Service, rbacRepo *rbac.Repository) *AdminService {
	return &AdminService{users: users, rbac: rbacRepo}
}

func (s *AdminService) GetUser(ctx context.Context, id int) (*UserInfoResp, error) {
	return s.GetUserForAdmin(ctx, id, nil)
}

// GetUserForAdmin is GetUser scoped to a delegated service admin's viewScopes
// (Task #13). When viewScopes is non-empty, the user must have at least one
// grant in one of those services or the call returns ErrNotFound (404 to the
// caller — leaking existence would be a privacy hole).
func (s *AdminService) GetUserForAdmin(ctx context.Context, id int, viewScopes []string) (*UserInfoResp, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: user %d not found", consts.ErrNotFound, id)
		}
		return nil, err
	}
	if len(viewScopes) > 0 {
		visible, err := s.rbac.UserHasGrantInServices(id, viewScopes)
		if err != nil {
			return nil, err
		}
		if !visible {
			return nil, fmt.Errorf("%w: user %d not found", consts.ErrNotFound, id)
		}
	}
	return NewUserInfoResp(u), nil
}

func (s *AdminService) GetUsersBatch(ctx context.Context, ids []int) (map[string]*UserInfoResp, error) {
	return s.GetUsersBatchForAdmin(ctx, ids, nil)
}

// GetUsersBatchForAdmin is GetUsersBatch scoped to viewScopes (Task #13).
// Users without any grant in viewScopes are silently absent.
func (s *AdminService) GetUsersBatchForAdmin(ctx context.Context, ids []int, viewScopes []string) (map[string]*UserInfoResp, error) {
	users, err := s.users.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	var visible map[int]struct{}
	if len(viewScopes) > 0 {
		ids := make([]int, 0, len(users))
		for _, u := range users {
			ids = append(ids, u.ID)
		}
		v, err := s.rbac.UsersWithGrantInServices(ids, viewScopes)
		if err != nil {
			return nil, err
		}
		visible = v
	}
	out := make(map[string]*UserInfoResp, len(users))
	for _, u := range users {
		if visible != nil {
			if _, ok := visible[u.ID]; !ok {
				continue
			}
		}
		out[fmt.Sprintf("%d", u.ID)] = NewUserInfoResp(u)
	}
	return out, nil
}

// ListUsersForAdmin lists users visible to the calling admin. Global admins
// (viewScopes==nil) see all users; service admins (Task #13) see only users
// who have at least one grant on a role with permissions in their admin
// services.
func (s *AdminService) ListUsersForAdmin(ctx context.Context, req *ListUsersReq, viewScopes []string) (*ListUsersResp, error) {
	return s.listUsersImpl(ctx, req, viewScopes)
}

func (s *AdminService) ListUsers(ctx context.Context, req *ListUsersReq) (*ListUsersResp, error) {
	return s.listUsersImpl(ctx, req, nil)
}

func (s *AdminService) listUsersImpl(ctx context.Context, req *ListUsersReq, viewScopes []string) (*ListUsersResp, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	size := req.PageSize
	switch {
	case size <= 0:
		size = int(consts.PageSizeLarge)
	case size <= 5:
		size = int(consts.PageSizeTiny)
	case size <= 10:
		size = int(consts.PageSizeSmall)
	case size <= 20:
		size = int(consts.PageSizeMedium)
	case size <= 50:
		size = int(consts.PageSizeLarge)
	default:
		size = int(consts.PageSizeXLarge)
	}
	req.PageSize = size
	innerReq := &user.ListUserReq{
		IsActive: req.IsActive,
		Status:   req.Status,
	}
	innerReq.Page = req.Page
	innerReq.Size = consts.PageSize(size)
	list, err := s.users.ListUsersScoped(ctx, innerReq, viewScopes)
	if err != nil {
		return nil, err
	}
	out := &ListUsersResp{
		Users:    make([]*UserInfoResp, 0, len(list.Items)),
		Total:    list.Pagination.Total,
		Page:     req.Page,
		PageSize: req.PageSize,
	}
	for i := range list.Items {
		item := list.Items[i]
		out.Users = append(out.Users, &UserInfoResp{
			ID:          item.ID,
			Username:    item.Username,
			Email:       item.Email,
			FullName:    item.FullName,
			Avatar:      item.Avatar,
			IsActive:    item.IsActive,
			LastLoginAt: item.LastLoginAt,
			CreatedAt:   item.CreatedAt,
		})
	}
	return out, nil
}

func (s *AdminService) Check(_ context.Context, req *CheckReq) (*CheckResp, error) {
	allowed, reason, err := s.rbac.CheckPermission(req.UserID, req.Permission, req.ScopeType, req.ScopeID)
	if err != nil {
		return nil, err
	}
	return &CheckResp{Allowed: allowed, Reason: reason}, nil
}

func (s *AdminService) CheckBatch(ctx context.Context, req *BatchCheckReq) ([]*CheckResp, error) {
	out := make([]*CheckResp, 0, len(req.Checks))
	for i := range req.Checks {
		item := req.Checks[i]
		if err := item.Validate(); err != nil {
			return nil, fmt.Errorf("%w: check[%d]: %v", consts.ErrBadRequest, i, err)
		}
		resp, err := s.Check(ctx, &item)
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return out, nil
}

// RegisterPermissionsForAdmin is RegisterPermissions enforcing the
// service-admin scope check (Task #13): a service admin may only register
// permissions for services in their admin set.
func (s *AdminService) RegisterPermissionsForAdmin(ctx context.Context, req *RegisterPermissionsReq, adminScopes []string, globalAdmin bool) (*RegisterPermissionsResp, error) {
	if !globalAdmin && !serviceInScopes(req.Service, adminScopes) {
		return nil, fmt.Errorf("%w: not service admin for %q", consts.ErrPermissionDenied, req.Service)
	}
	return s.RegisterPermissions(ctx, req)
}

func (s *AdminService) RegisterPermissions(_ context.Context, req *RegisterPermissionsReq) (*RegisterPermissionsResp, error) {
	resp := &RegisterPermissionsResp{}
	for i := range req.Permissions {
		p := req.Permissions[i]
		created, conflictingService, err := s.rbac.UpsertPermission(p.Name, p.DisplayName, p.Description, req.Service, p.ScopeType)
		if err != nil {
			return nil, err
		}
		if conflictingService != "" {
			return nil, fmt.Errorf("%w: permission %q owned by service %q", consts.ErrAlreadyExists, p.Name, conflictingService)
		}
		if created {
			resp.Registered++
		} else {
			resp.Updated++
		}
	}
	return resp, nil
}

func (s *AdminService) resolveRole(req *GrantReq) (*model.Role, error) {
	if req.RoleID > 0 {
		role, err := s.rbac.FindRoleByID(req.RoleID)
		if err != nil {
			return nil, fmt.Errorf("%w: role %d not found", consts.ErrNotFound, req.RoleID)
		}
		return role, nil
	}
	role, err := s.rbac.FindRoleByName(req.Role)
	if err != nil {
		return nil, fmt.Errorf("%w: role %q not found", consts.ErrNotFound, req.Role)
	}
	return role, nil
}

func (s *AdminService) GrantScopedRole(ctx context.Context, req *GrantReq) (*GrantResp, error) {
	return s.GrantScopedRoleForAdmin(ctx, req, nil, true)
}

// GrantScopedRoleForAdmin is GrantScopedRole enforcing the service-admin
// scope check (Task #13). The grant is allowed when:
//   - globalAdmin is true (system_admin bypass), OR
//   - the request's scope_type is ScopeTypeService AND scope_id is in
//     adminScopes, OR
//   - every service that the target role's permissions belong to is in
//     adminScopes (so service admins can hand out roles whose effect is
//     confined to their service).
func (s *AdminService) GrantScopedRoleForAdmin(_ context.Context, req *GrantReq, adminScopes []string, globalAdmin bool) (*GrantResp, error) {
	role, err := s.resolveRole(req)
	if err != nil {
		return nil, err
	}
	if !globalAdmin {
		if err := s.authorizeRoleGrant(role.ID, req, adminScopes); err != nil {
			return nil, err
		}
	}
	created, err := s.rbac.AssignScopedRole(req.UserID, role.ID, req.ScopeType, req.ScopeID)
	if err != nil {
		return nil, err
	}
	return &GrantResp{Granted: created}, nil
}

func (s *AdminService) RevokeScopedRole(ctx context.Context, req *GrantReq) (*RevokeResp, error) {
	return s.RevokeScopedRoleForAdmin(ctx, req, nil, true)
}

func (s *AdminService) RevokeScopedRoleForAdmin(_ context.Context, req *GrantReq, adminScopes []string, globalAdmin bool) (*RevokeResp, error) {
	role, err := s.resolveRole(req)
	if err != nil {
		return nil, err
	}
	if !globalAdmin {
		if err := s.authorizeRoleGrant(role.ID, req, adminScopes); err != nil {
			return nil, err
		}
	}
	rows, err := s.rbac.RevokeScopedRole(req.UserID, role.ID, req.ScopeType, req.ScopeID)
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, fmt.Errorf("%w: no active grant matched", consts.ErrNotFound)
	}
	return &RevokeResp{Revoked: true}, nil
}

func (s *AdminService) authorizeRoleGrant(roleID int, req *GrantReq, adminScopes []string) error {
	// service admin granting another service-admin grant: must be a service
	// they admin.
	if req.ScopeType == consts.ScopeTypeService {
		if !serviceInScopes(req.ScopeID, adminScopes) {
			return fmt.Errorf("%w: not service admin for %q", consts.ErrPermissionDenied, req.ScopeID)
		}
		return nil
	}
	// generic business-scope grant: the role's permissions must be confined
	// to the caller's admin services.
	services, err := s.rbac.ListRolePermissionServices(roleID)
	if err != nil {
		return err
	}
	for _, svc := range services {
		if !serviceInScopes(svc, adminScopes) {
			return fmt.Errorf("%w: role grants permissions in service %q outside admin scope", consts.ErrPermissionDenied, svc)
		}
	}
	return nil
}

func serviceInScopes(service string, scopes []string) bool {
	for _, s := range scopes {
		if s == service {
			return true
		}
	}
	return false
}

func (s *AdminService) ListUserGrants(_ context.Context, userID int, scopeType, service string) ([]*UserGrantResp, error) {
	rows, err := s.rbac.ListUserScopedRoles(userID, scopeType, service)
	if err != nil {
		return nil, err
	}
	out := make([]*UserGrantResp, 0, len(rows))
	for _, r := range rows {
		out = append(out, &UserGrantResp{
			Role:      r.RoleName,
			ScopeType: r.ScopeType,
			ScopeID:   r.ScopeID,
			GrantedAt: r.CreatedAt,
		})
	}
	return out, nil
}

func (s *AdminService) ListScopeUsers(_ context.Context, scopeType, scopeID string) ([]*ScopeUserResp, error) {
	rows, err := s.rbac.ListScopeUsers(scopeType, scopeID)
	if err != nil {
		return nil, err
	}
	out := make([]*ScopeUserResp, 0, len(rows))
	for _, r := range rows {
		out = append(out, &ScopeUserResp{
			UserID:   r.UserID,
			Username: r.Username,
			Role:     r.RoleName,
		})
	}
	return out, nil
}
