package rbac

import (
	"aegis/platform/consts"
	"aegis/platform/model"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) createRoleRecord(role *model.Role) error {
	if err := r.db.Create(role).Error; err != nil {
		return fmt.Errorf("failed to create role: %w", err)
	}
	return nil
}

func (r *Repository) deleteRoleCascade(roleID int) (int64, error) {
	role, err := r.loadRole(roleID)
	if err != nil {
		return 0, err
	}
	if role.IsSystem {
		return 0, fmt.Errorf("%w: cannot delete system role", consts.ErrPermissionDenied)
	}

	if err := r.db.Model(&model.UserScopedRole{}).
		Where("role_id = ? AND status != ?", role.ID, consts.CommonDeleted).
		Update("status", consts.CommonDeleted).Error; err != nil {
		return 0, fmt.Errorf("failed to remove scoped role grants: %w", err)
	}
	if err := r.db.Where("role_id = ?", role.ID).Delete(&model.RolePermission{}).Error; err != nil {
		return 0, fmt.Errorf("failed to remove permissions with role: %w", err)
	}
	if err := r.db.Where("role_id = ?", role.ID).Delete(&model.UserRole{}).Error; err != nil {
		return 0, fmt.Errorf("failed to remove users with role: %w", err)
	}

	result := r.db.Model(&model.Role{}).
		Where("id = ? AND status != ?", role.ID, consts.CommonDeleted).
		Update("status", consts.CommonDeleted)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to delete role %d: %w", role.ID, result.Error)
	}
	return result.RowsAffected, nil
}

func (r *Repository) loadRoleDetail(roleID int) (*model.Role, int64, []model.Permission, error) {
	role, err := r.loadRole(roleID)
	if err != nil {
		return nil, 0, nil, err
	}

	var userCount int64
	if err := r.db.Table("users").
		Joins("JOIN user_roles ON users.id = user_roles.user_id").
		Where("user_roles.role_id = ? AND users.status = ?", role.ID, consts.CommonEnabled).
		Count(&userCount).Error; err != nil {
		return nil, 0, nil, fmt.Errorf("failed to get role user count: %w", err)
	}

	var permissions []model.Permission
	if err := r.db.Table("permissions").
		Joins("JOIN role_permissions ON permissions.id = role_permissions.permission_id").
		Where("role_permissions.role_id = ? AND permissions.status = ?", role.ID, consts.CommonEnabled).
		Find(&permissions).Error; err != nil {
		return nil, 0, nil, fmt.Errorf("failed to get role permissions: %w", err)
	}

	return role, userCount, permissions, nil
}

func (r *Repository) listRoleViews(limit, offset int, isSystem *bool, status *consts.StatusType) ([]model.Role, int64, error) {
	var roles []model.Role
	var total int64

	query := r.db.Model(&model.Role{})
	if isSystem != nil {
		query = query.Where("is_system = ?", *isSystem)
	}
	if status != nil {
		query = query.Where("status = ?", *status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count roles: %v", err)
	}
	if err := query.Limit(limit).Offset(offset).Order("updated_at DESC").Find(&roles).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list roles: %v", err)
	}
	return roles, total, nil
}

func (r *Repository) updateMutableRole(roleID int, patch func(*model.Role)) (*model.Role, error) {
	role, err := r.loadRole(roleID)
	if err != nil {
		return nil, err
	}
	if role.IsSystem {
		return nil, fmt.Errorf("%w: cannot update system role", consts.ErrPermissionDenied)
	}

	patch(role)
	if err := r.db.Omit("ActiveName").Save(role).Error; err != nil {
		return nil, fmt.Errorf("failed to update role: %w", err)
	}
	return role, nil
}

func (r *Repository) assignRolePermissions(roleID int, permissionIDs []int) error {
	role, err := r.loadRole(roleID)
	if err != nil {
		return err
	}
	if role.IsSystem {
		return fmt.Errorf("%w: cannot assign permissions to system role", consts.ErrPermissionDenied)
	}

	permissionMap, err := r.buildAssignablePermissionMap(permissionIDs)
	if err != nil {
		return err
	}

	rolePermissions := make([]model.RolePermission, 0, len(permissionIDs))
	for _, permissionID := range permissionIDs {
		if _, exists := permissionMap[permissionID]; !exists {
			return fmt.Errorf("%w: permission id %d not found", consts.ErrNotFound, permissionID)
		}
		rolePermissions = append(rolePermissions, model.RolePermission{
			RoleID:       role.ID,
			PermissionID: permissionID,
		})
	}

	if len(rolePermissions) == 0 {
		return nil
	}
	if err := r.db.Create(&rolePermissions).Error; err != nil {
		return fmt.Errorf("failed to batch create role permissions: %w", err)
	}
	return nil
}

func (r *Repository) removeRolePermissions(roleID int, permissionIDs []int) error {
	role, err := r.loadRole(roleID)
	if err != nil {
		return err
	}
	if role.IsSystem {
		return fmt.Errorf("%w: cannot remove permissions of system role", consts.ErrPermissionDenied)
	}

	permissionMap, err := r.buildAssignablePermissionMap(permissionIDs)
	if err != nil {
		return err
	}
	for _, permissionID := range permissionIDs {
		if _, exists := permissionMap[permissionID]; !exists {
			return fmt.Errorf("%w: permission id %d not found", consts.ErrNotFound, permissionID)
		}
	}

	if len(permissionIDs) == 0 {
		return nil
	}
	if err := r.db.Where("role_id = ? AND permission_id IN (?)", role.ID, permissionIDs).
		Delete(&model.RolePermission{}).Error; err != nil {
		return fmt.Errorf("failed to batch delete role permissions: %w", err)
	}
	return nil
}

func (r *Repository) listUsersFromRole(roleID int) (*model.Role, []model.User, error) {
	role, err := r.loadRole(roleID)
	if err != nil {
		return nil, nil, err
	}

	var users []model.User
	if err := r.db.Table("users").
		Joins("JOIN user_roles ON users.id = user_roles.user_id").
		Where("user_roles.role_id = ? AND users.status = ?", role.ID, consts.CommonEnabled).
		Find(&users).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to get role users: %w", err)
	}
	return role, users, nil
}

func (r *Repository) getPermissionDetail(permissionID int) (*model.Permission, error) {
	var permission model.Permission
	if err := r.db.
		Where("id = ? and status != ?", permissionID, consts.CommonDeleted).
		First(&permission).Error; err != nil {
		return nil, fmt.Errorf("failed to find permission with id %d: %w", permissionID, err)
	}
	return &permission, nil
}

func (r *Repository) listPermissionViews(limit, offset int, action consts.ActionName, isSystem *bool, status *consts.StatusType) ([]model.Permission, int64, error) {
	var permissions []model.Permission
	var total int64

	query := r.db.Model(&model.Permission{})
	if action != "" {
		query = query.Where("action = ?", action)
	}
	if isSystem != nil {
		query = query.Where("is_system = ?", *isSystem)
	}
	if status != nil {
		query = query.Where("status = ?", *status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count permissions: %v", err)
	}
	if err := query.Limit(limit).Offset(offset).Order("updated_at DESC").Find(&permissions).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list permissions: %v", err)
	}
	return permissions, total, nil
}

func (r *Repository) listRolesFromPermission(permissionID int) (*model.Permission, []model.Role, error) {
	permission, err := r.getPermissionDetail(permissionID)
	if err != nil {
		return nil, nil, err
	}

	var roles []model.Role
	if err := r.db.Table("roles").
		Joins("JOIN role_permissions ON roles.id = role_permissions.role_id").
		Where("role_permissions.permission_id = ? AND roles.status != ?", permission.ID, consts.CommonDeleted).
		Find(&roles).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to get permission roles: %w", err)
	}
	return permission, roles, nil
}

func (r *Repository) getResourceDetail(resourceID int) (*model.Resource, error) {
	var resource model.Resource
	if err := r.db.
		Where("id = ? and status != ?", resourceID, consts.CommonDeleted).
		First(&resource).Error; err != nil {
		return nil, fmt.Errorf("failed to find resource with id %d: %w", resourceID, err)
	}
	return &resource, nil
}

func (r *Repository) listResourceViews(limit, offset int, resourceType *consts.ResourceType, category *consts.ResourceCategory) ([]model.Resource, int64, error) {
	var resources []model.Resource
	var total int64

	query := r.db.Model(&model.Resource{}).Preload("Parent")
	if resourceType != nil {
		query = query.Where("type = ?", *resourceType)
	}
	if category != nil {
		query = query.Where("category = ?", *category)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count resources: %v", err)
	}
	if err := query.Limit(limit).Offset(offset).Find(&resources).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list resources: %v", err)
	}
	return resources, total, nil
}

func (r *Repository) listResourcePermissions(resourceID int) (*model.Resource, []model.Permission, error) {
	resource, err := r.getResourceDetail(resourceID)
	if err != nil {
		return nil, nil, err
	}

	// Permission rows no longer FK to resources after the SSO extraction
	// schema collapse; the Resource model survives as a standalone catalog.
	return resource, nil, nil
}

func (r *Repository) loadRole(roleID int) (*model.Role, error) {
	var role model.Role
	if err := r.db.Where("id = ? and status != ?", roleID, consts.CommonDeleted).First(&role).Error; err != nil {
		return nil, fmt.Errorf("failed to find role with id %d: %w", roleID, err)
	}
	return &role, nil
}

func (r *Repository) listPermissionsByIDs(permissionIDs []int) ([]model.Permission, error) {
	if len(permissionIDs) == 0 {
		return []model.Permission{}, nil
	}

	var permissions []model.Permission
	if err := r.db.Where("id IN (?) AND status = ?", permissionIDs, consts.CommonEnabled).
		Find(&permissions).Error; err != nil {
		return nil, fmt.Errorf("failed to query permissions: %w", err)
	}
	return permissions, nil
}

// CheckPermission resolves whether userID has permName, optionally constrained
// to (scopeType, scopeID). For scopeType == "" the global user_roles path is
// consulted PLUS any user_scoped_roles row whose scope_type=ScopeTypeService —
// a service-admin grant counts as "allowed to attempt"; the handler is then
// responsible for restricting the data to scope_id services (Task #13). For
// a non-empty scopeType, the union of global roles + user_scoped_roles at
// (scopeType, scopeID) is consulted. Returns the granting role name in
// `reason` (as "role:<name>") on allow, or "denied" otherwise.
func (r *Repository) CheckPermission(userID int, permName, scopeType, scopeID string) (bool, string, error) {
	var perm model.Permission
	if err := r.db.
		Where("name = ? AND status >= ?", permName, consts.CommonDisabled).
		First(&perm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, "denied", nil
		}
		return false, "", fmt.Errorf("failed to find permission %q: %w", permName, err)
	}

	type row struct {
		RoleName string
	}

	globalQ := r.db.
		Select("roles.name AS role_name").
		Table("role_permissions rp").
		Joins("JOIN user_roles ur ON rp.role_id = ur.role_id").
		Joins("JOIN roles ON roles.id = rp.role_id").
		Where("ur.user_id = ? AND rp.permission_id = ?", userID, perm.ID).
		Where("roles.status = ?", consts.CommonEnabled)

	if scopeType == "" {
		var hit row
		if err := globalQ.Limit(1).Scan(&hit).Error; err != nil {
			return false, "", fmt.Errorf("failed to check global permission: %w", err)
		}
		if hit.RoleName != "" {
			return true, "role:" + hit.RoleName, nil
		}

		// Service-admin fallback (Task #13): a user with any active
		// ScopeTypeService grant transitively holding this permission is
		// allowed to *attempt* the operation. The handler then filters
		// the response to the user's admin services.
		var saHit row
		if err := r.db.
			Select("roles.name AS role_name").
			Table("role_permissions rp").
			Joins("JOIN user_scoped_roles usr ON rp.role_id = usr.role_id").
			Joins("JOIN roles ON roles.id = rp.role_id").
			Where("usr.user_id = ? AND rp.permission_id = ?", userID, perm.ID).
			Where("usr.scope_type = ?", consts.ScopeTypeService).
			Where("usr.scope_id <> ''").
			Where("usr.status = ?", consts.CommonEnabled).
			Where("roles.status = ?", consts.CommonEnabled).
			Limit(1).Scan(&saHit).Error; err != nil {
			return false, "", fmt.Errorf("failed to check service-admin permission: %w", err)
		}
		if saHit.RoleName != "" {
			return true, "role:" + saHit.RoleName, nil
		}
		return false, "denied", nil
	}

	scopedQ := r.db.
		Select("roles.name AS role_name").
		Table("role_permissions rp").
		Joins("JOIN user_scoped_roles usr ON rp.role_id = usr.role_id").
		Joins("JOIN roles ON roles.id = rp.role_id").
		Where("usr.user_id = ? AND usr.scope_type = ? AND usr.scope_id = ? AND rp.permission_id = ?",
			userID, scopeType, scopeID, perm.ID).
		Where("usr.status = ?", consts.CommonEnabled).
		Where("roles.status = ?", consts.CommonEnabled)

	var hits []row
	if err := r.db.
		Table("(?) AS combined", r.db.Raw("? UNION ALL ?", globalQ, scopedQ)).
		Limit(1).
		Scan(&hits).Error; err != nil {
		return false, "", fmt.Errorf("failed to check scoped permission: %w", err)
	}
	if len(hits) > 0 && hits[0].RoleName != "" {
		return true, "role:" + hits[0].RoleName, nil
	}
	return false, "denied", nil
}

// AssignScopedRole upserts a scoped role grant. If a row exists with status
// CommonDeleted it's reactivated; if it exists active, it's left alone.
// Returns (created bool, error).
func (r *Repository) AssignScopedRole(userID, roleID int, scopeType, scopeID string) (bool, error) {
	var existing model.UserScopedRole
	err := r.db.
		Where("user_id = ? AND role_id = ? AND scope_type = ? AND scope_id = ?",
			userID, roleID, scopeType, scopeID).
		First(&existing).Error
	if err == nil {
		if existing.Status == consts.CommonEnabled {
			return false, nil
		}
		if err := r.db.Model(&existing).
			Update("status", consts.CommonEnabled).Error; err != nil {
			return false, fmt.Errorf("failed to reactivate scoped role: %w", err)
		}
		return true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, fmt.Errorf("failed to query scoped role: %w", err)
	}

	row := &model.UserScopedRole{
		UserID:    userID,
		RoleID:    roleID,
		ScopeType: scopeType,
		ScopeID:   scopeID,
		Status:    consts.CommonEnabled,
	}
	if err := r.db.Create(row).Error; err != nil {
		return false, fmt.Errorf("failed to create scoped role: %w", err)
	}
	return true, nil
}

// RevokeScopedRole soft-deletes a scoped role grant. Returns rows affected.
func (r *Repository) RevokeScopedRole(userID, roleID int, scopeType, scopeID string) (int64, error) {
	result := r.db.Model(&model.UserScopedRole{}).
		Where("user_id = ? AND role_id = ? AND scope_type = ? AND scope_id = ? AND status != ?",
			userID, roleID, scopeType, scopeID, consts.CommonDeleted).
		Update("status", consts.CommonDeleted)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to revoke scoped role: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// ListUserScopedRoles returns the user's active scoped role grants joined to
// the role row, optionally filtered by scope_type and/or service (which
// requires permissions join).
func (r *Repository) ListUserScopedRoles(userID int, scopeType, service string) ([]ScopedRoleRow, error) {
	q := r.db.
		Table("user_scoped_roles usr").
		Select("usr.scope_type, usr.scope_id, usr.created_at, roles.name AS role_name, roles.id AS role_id").
		Joins("JOIN roles ON roles.id = usr.role_id").
		Where("usr.user_id = ? AND usr.status = ?", userID, consts.CommonEnabled).
		Where("roles.status = ?", consts.CommonEnabled)

	if scopeType != "" {
		q = q.Where("usr.scope_type = ?", scopeType)
	}
	if service != "" {
		q = q.Joins("JOIN role_permissions rp ON rp.role_id = roles.id").
			Joins("JOIN permissions p ON p.id = rp.permission_id").
			Where("p.service = ? AND p.status >= ?", service, consts.CommonDisabled).
			Group("usr.id, usr.scope_type, usr.scope_id, usr.created_at, roles.name, roles.id")
	}

	var rows []ScopedRoleRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list user scoped roles: %w", err)
	}
	return rows, nil
}

// ListScopeUsers returns who has any role at (scope_type, scope_id).
func (r *Repository) ListScopeUsers(scopeType, scopeID string) ([]ScopeUserRow, error) {
	var rows []ScopeUserRow
	if err := r.db.
		Table("user_scoped_roles usr").
		Select("usr.user_id, users.username, roles.name AS role_name").
		Joins("JOIN users ON users.id = usr.user_id").
		Joins("JOIN roles ON roles.id = usr.role_id").
		Where("usr.scope_type = ? AND usr.scope_id = ? AND usr.status = ?",
			scopeType, scopeID, consts.CommonEnabled).
		Where("users.status = ?", consts.CommonEnabled).
		Where("roles.status = ?", consts.CommonEnabled).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list scope users: %w", err)
	}
	return rows, nil
}

// ListServiceAdminScopes returns the distinct service names (scope_id values)
// the user is service-admin for — i.e. has any active user_scoped_roles row
// with scope_type=ScopeTypeService. An empty list means the user is NOT a
// service admin (they may still be a global admin; the caller checks that
// separately). Task #13.
func (r *Repository) ListServiceAdminScopes(userID int) ([]string, error) {
	var scopes []string
	if err := r.db.
		Table("user_scoped_roles").
		Select("DISTINCT scope_id").
		Where("user_id = ? AND scope_type = ? AND status = ? AND scope_id <> ''",
			userID, consts.ScopeTypeService, consts.CommonEnabled).
		Scan(&scopes).Error; err != nil {
		return nil, fmt.Errorf("failed to list service-admin scopes: %w", err)
	}
	return scopes, nil
}

// UserHasGrantInServices reports whether the user has at least one active
// user_scoped_roles row whose role grants permissions in any of `services`.
// Used by the SSO /v1/users handlers to enforce delegated service-admin
// visibility (Task #13).
func (r *Repository) UserHasGrantInServices(userID int, services []string) (bool, error) {
	if len(services) == 0 {
		return false, nil
	}
	var count int64
	if err := r.db.
		Table("user_scoped_roles usr").
		Joins("JOIN role_permissions rp ON rp.role_id = usr.role_id").
		Joins("JOIN permissions p ON p.id = rp.permission_id").
		Where("usr.user_id = ? AND usr.status = ? AND p.service IN ? AND p.status >= ?",
			userID, consts.CommonEnabled, services, consts.CommonDisabled).
		Limit(1).Count(&count).Error; err != nil {
		return false, fmt.Errorf("failed to check user service grants: %w", err)
	}
	return count > 0, nil
}

// UsersWithGrantInServices returns the subset of userIDs that have any
// active grant in any of services (same semantics as UserHasGrantInServices,
// batched). Used by the SSO /v1/users:batch handler.
func (r *Repository) UsersWithGrantInServices(userIDs []int, services []string) (map[int]struct{}, error) {
	out := make(map[int]struct{})
	if len(userIDs) == 0 || len(services) == 0 {
		return out, nil
	}
	var rows []struct{ UserID int }
	if err := r.db.
		Table("user_scoped_roles usr").
		Select("DISTINCT usr.user_id AS user_id").
		Joins("JOIN role_permissions rp ON rp.role_id = usr.role_id").
		Joins("JOIN permissions p ON p.id = rp.permission_id").
		Where("usr.user_id IN ? AND usr.status = ? AND p.service IN ? AND p.status >= ?",
			userIDs, consts.CommonEnabled, services, consts.CommonDisabled).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list users with service grants: %w", err)
	}
	for _, r := range rows {
		out[r.UserID] = struct{}{}
	}
	return out, nil
}

// ListRolePermissionServices returns the distinct services of the permissions
// granted by the given role. Used by the SSO admin gate to decide whether a
// service admin is allowed to grant the role: all of the role's permission
// services must be in the caller's admin set.
func (r *Repository) ListRolePermissionServices(roleID int) ([]string, error) {
	var services []string
	if err := r.db.
		Table("permissions p").
		Select("DISTINCT p.service").
		Joins("JOIN role_permissions rp ON rp.permission_id = p.id").
		Where("rp.role_id = ? AND p.status >= ?", roleID, consts.CommonDisabled).
		Scan(&services).Error; err != nil {
		return nil, fmt.Errorf("failed to list role permission services: %w", err)
	}
	return services, nil
}

// ScopedRoleRow is the projection returned by ListUserScopedRoles.
type ScopedRoleRow struct {
	RoleID    int    `gorm:"column:role_id"`
	RoleName  string `gorm:"column:role_name"`
	ScopeType string `gorm:"column:scope_type"`
	ScopeID   string `gorm:"column:scope_id"`
	CreatedAt time.Time
}

// ScopeUserRow is the projection returned by ListScopeUsers.
type ScopeUserRow struct {
	UserID   int    `gorm:"column:user_id"`
	Username string `gorm:"column:username"`
	RoleName string `gorm:"column:role_name"`
}

// FindRoleByName loads a role by its (active) name.
func (r *Repository) FindRoleByName(name string) (*model.Role, error) {
	var role model.Role
	if err := r.db.Where("name = ? AND status >= ?", name, consts.CommonDisabled).
		First(&role).Error; err != nil {
		return nil, err
	}
	return &role, nil
}

// FindRoleByID loads an active role by ID.
func (r *Repository) FindRoleByID(id int) (*model.Role, error) {
	return r.loadRole(id)
}

// UpsertPermission inserts the permission row or updates display_name /
// description / scope_type when it already exists. The unique key is (name).
// Returns (created bool, conflicting service string if name owned by another
// service; "" otherwise).
func (r *Repository) UpsertPermission(name, displayName, description, service, scopeType string) (bool, string, error) {
	var existing model.Permission
	err := r.db.Where("name = ?", name).First(&existing).Error
	if err == nil {
		if existing.Service != "" && existing.Service != service {
			return false, existing.Service, nil
		}
		updates := map[string]any{
			"display_name": displayName,
			"description":  description,
			"scope_type":   scopeType,
		}
		if existing.Status < consts.CommonDisabled {
			updates["status"] = consts.CommonEnabled
		}
		if err := r.db.Model(&existing).Updates(updates).Error; err != nil {
			return false, "", fmt.Errorf("failed to update permission %q: %w", name, err)
		}
		return false, "", nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, "", fmt.Errorf("failed to query permission %q: %w", name, err)
	}

	row := &model.Permission{
		Name:        name,
		DisplayName: displayName,
		Description: description,
		Service:     service,
		ScopeType:   scopeType,
		Status:      consts.CommonEnabled,
	}
	if err := r.db.Omit("ActiveName").Create(row).Error; err != nil {
		return false, "", fmt.Errorf("failed to create permission %q: %w", name, err)
	}
	return true, "", nil
}

func (r *Repository) buildAssignablePermissionMap(permissionIDs []int) (map[int]model.Permission, error) {
	if len(permissionIDs) == 0 {
		return map[int]model.Permission{}, nil
	}

	unique := make(map[int]struct{}, len(permissionIDs))
	for _, id := range permissionIDs {
		unique[id] = struct{}{}
	}

	deduplicatedIDs := make([]int, 0, len(unique))
	for id := range unique {
		deduplicatedIDs = append(deduplicatedIDs, id)
	}

	permissions, err := r.listPermissionsByIDs(deduplicatedIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to list permissions by ids: %w", err)
	}

	result := make(map[int]model.Permission, len(permissions))
	for _, permission := range permissions {
		result[permission.ID] = permission
	}
	return result, nil
}
