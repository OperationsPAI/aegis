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
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: user %d not found", consts.ErrNotFound, id)
		}
		return nil, err
	}
	return NewUserInfoResp(u), nil
}

func (s *AdminService) GetUsersBatch(ctx context.Context, ids []int) (map[string]*UserInfoResp, error) {
	users, err := s.users.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*UserInfoResp, len(users))
	for _, u := range users {
		out[fmt.Sprintf("%d", u.ID)] = NewUserInfoResp(u)
	}
	return out, nil
}

func (s *AdminService) ListUsers(ctx context.Context, req *ListUsersReq) (*ListUsersResp, error) {
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
	list, err := s.users.ListUsers(ctx, innerReq)
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

func (s *AdminService) GrantScopedRole(_ context.Context, req *GrantReq) (*GrantResp, error) {
	role, err := s.resolveRole(req)
	if err != nil {
		return nil, err
	}
	created, err := s.rbac.AssignScopedRole(req.UserID, role.ID, req.ScopeType, req.ScopeID)
	if err != nil {
		return nil, err
	}
	return &GrantResp{Granted: created}, nil
}

func (s *AdminService) RevokeScopedRole(_ context.Context, req *GrantReq) (*RevokeResp, error) {
	role, err := s.resolveRole(req)
	if err != nil {
		return nil, err
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
