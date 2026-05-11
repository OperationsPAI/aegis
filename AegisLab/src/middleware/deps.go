package middleware

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"aegis/consts"
	"aegis/dto"
	"aegis/model"
	"aegis/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type TokenVerifier interface {
	VerifyToken(ctx context.Context, token string) (*utils.Claims, error)
	VerifyServiceToken(ctx context.Context, token string) (*utils.ServiceClaims, error)
}

type permissionChecker interface {
	CheckUserPermission(context.Context, *dto.CheckPermissionParams) (bool, error)
	IsUserTeamAdmin(context.Context, int, int) (bool, error)
	IsUserInTeam(context.Context, int, int) (bool, error)
	IsTeamPublic(context.Context, int) (bool, error)
	IsUserProjectAdmin(context.Context, int, int) (bool, error)
	IsUserInProject(context.Context, int, int) (bool, error)
}

type auditLogger interface {
	LogFailedAction(ipAddress, userAgent, action, errorMsg string, duration, userID int, resourceName consts.ResourceName) error
	LogUserAction(ipAddress, userAgent, action, details string, duration, userID int, resourceName consts.ResourceName) error
}

type Service interface {
	TokenVerifier
	permissionChecker
	auditLogger
}

const middlewareServiceContextKey = "middleware.service"

func NewService(db *gorm.DB, verifier TokenVerifier) Service {
	return &dbBackedMiddlewareService{db: db, verifier: verifier}
}

func InjectService(service Service) gin.HandlerFunc {
	if service == nil {
		service = noopMiddlewareService{}
	}

	return func(c *gin.Context) {
		c.Set(middlewareServiceContextKey, service)
		c.Next()
	}
}

func permissionCheckerFromContext(c *gin.Context) permissionChecker { return serviceFromContext(c) }

func auditLoggerFromContext(c *gin.Context) auditLogger { return serviceFromContext(c) }

func serviceFromContext(c *gin.Context) Service {
	if c == nil {
		return noopMiddlewareService{}
	}
	service, ok := c.Get(middlewareServiceContextKey)
	if !ok {
		return noopMiddlewareService{}
	}
	middlewareService, ok := service.(Service)
	if !ok || middlewareService == nil {
		return noopMiddlewareService{}
	}
	return middlewareService
}

type dbBackedMiddlewareService struct {
	db       *gorm.DB
	verifier TokenVerifier
}

func (s *dbBackedMiddlewareService) VerifyToken(ctx context.Context, token string) (*utils.Claims, error) {
	if s.verifier == nil {
		return nil, fmt.Errorf("token verifier not initialized")
	}
	return s.verifier.VerifyToken(ctx, token)
}

func (s *dbBackedMiddlewareService) VerifyServiceToken(ctx context.Context, token string) (*utils.ServiceClaims, error) {
	if s.verifier == nil {
		return nil, fmt.Errorf("token verifier not initialized")
	}
	return s.verifier.VerifyServiceToken(ctx, token)
}

func (s *dbBackedMiddlewareService) CheckUserPermission(_ context.Context, params *dto.CheckPermissionParams) (bool, error) {
	if err := params.Validate(); err != nil {
		return false, fmt.Errorf("invalid request: %w", err)
	}

	rule := consts.PermissionRule{Resource: params.ResourceName, Action: params.Action, Scope: params.Scope}
	permission, err := s.getPermissionByName(rule.String())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed to find target permission: %w", err)
	}

	return s.checkUserHasPermission(params, permission.ID)
}

func (s *dbBackedMiddlewareService) IsUserInTeam(_ context.Context, userID, teamID int) (bool, error) {
	ut, err := s.getScopedRole(userID, consts.ScopeTypeTeam, strconv.Itoa(teamID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return ut != nil, nil
}

func (s *dbBackedMiddlewareService) IsUserTeamAdmin(_ context.Context, userID, teamID int) (bool, error) {
	ut, err := s.getScopedRole(userID, consts.ScopeTypeTeam, strconv.Itoa(teamID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return ut != nil && ut.Role != nil && ut.Role.Name == consts.RoleTeamAdmin.String(), nil
}

func (s *dbBackedMiddlewareService) IsTeamPublic(_ context.Context, teamID int) (bool, error) {
	team, err := s.getTeamByID(teamID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return team.IsPublic, nil
}

func (s *dbBackedMiddlewareService) IsUserInProject(_ context.Context, userID, projectID int) (bool, error) {
	up, err := s.getScopedRole(userID, consts.ScopeTypeProject, strconv.Itoa(projectID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return up != nil, nil
}

func (s *dbBackedMiddlewareService) IsUserProjectAdmin(_ context.Context, userID, projectID int) (bool, error) {
	up, err := s.getScopedRole(userID, consts.ScopeTypeProject, strconv.Itoa(projectID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return up != nil && up.Role != nil && up.Role.Name == consts.RoleProjectAdmin.String(), nil
}

func (s *dbBackedMiddlewareService) LogFailedAction(ipAddress, userAgent, action, errorMsg string, duration, userID int, resourceName consts.ResourceName) error {
	if resourceName == "" {
		return fmt.Errorf("resource name cannot be empty")
	}

	log := &model.AuditLog{
		IPAddress: ipAddress,
		UserAgent: userAgent,
		Duration:  duration,
		Action:    action,
		ErrorMsg:  errorMsg,
		UserID:    userID,
		State:     consts.AuditLogStateFailed,
		Status:    consts.CommonEnabled,
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		resource, err := s.getResourceByName(tx, resourceName)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: resource %s not found", consts.ErrNotFound, resourceName)
			}
			return fmt.Errorf("failed to get resource: %w", err)
		}

		log.ResourceID = resource.ID
		return s.createAuditLog(tx, log)
	})
}

func (s *dbBackedMiddlewareService) LogUserAction(ipAddress, userAgent, action, details string, duration, userID int, resourceName consts.ResourceName) error {
	if resourceName == "" {
		return fmt.Errorf("resource name cannot be empty")
	}

	log := &model.AuditLog{
		IPAddress: ipAddress,
		UserAgent: userAgent,
		Duration:  duration,
		Action:    action,
		Details:   details,
		UserID:    userID,
		State:     consts.AuditLogStateSuccess,
		Status:    consts.CommonEnabled,
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		resource, err := s.getResourceByName(tx, resourceName)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: resource %s not found", consts.ErrNotFound, resourceName)
			}
			return fmt.Errorf("failed to get resource: %w", err)
		}

		log.ResourceID = resource.ID
		return s.createAuditLog(tx, log)
	})
}

func (s *dbBackedMiddlewareService) getPermissionByName(name string) (*model.Permission, error) {
	var permission model.Permission
	if err := s.db.
		Where("name = ? AND status != ?", name, consts.CommonDeleted).
		First(&permission).Error; err != nil {
		return nil, err
	}
	return &permission, nil
}

func (s *dbBackedMiddlewareService) checkUserHasPermission(params *dto.CheckPermissionParams, permissionID int) (bool, error) {
	directQuery := s.buildDirectPermissionQuery(params.UserID, permissionID, params.ProjectID, params.ContainerID, params.DatasetID)
	globalRoleQuery := s.buildGlobalRolePermissionQuery(params.UserID, permissionID)
	finalQuery := s.db.Table("(? UNION ALL ?) as base", directQuery, globalRoleQuery)

	if params.TeamID != nil {
		finalQuery = s.db.Table("(? UNION ALL ?) as combined", finalQuery,
			s.buildScopedRolePermissionQuery(params.UserID, permissionID, consts.ScopeTypeTeam, strconv.Itoa(*params.TeamID)))
	}
	if params.ProjectID != nil {
		finalQuery = s.db.Table("(? UNION ALL ?) as combined", finalQuery,
			s.buildScopedRolePermissionQuery(params.UserID, permissionID, consts.ScopeTypeProject, strconv.Itoa(*params.ProjectID)))
	}
	if params.ContainerID != nil {
		finalQuery = s.db.Table("(? UNION ALL ?) as combined", finalQuery,
			s.buildScopedRolePermissionQuery(params.UserID, permissionID, consts.ScopeTypeContainer, strconv.Itoa(*params.ContainerID)))
	}
	if params.DatasetID != nil {
		finalQuery = s.db.Table("(? UNION ALL ?) as combined", finalQuery,
			s.buildScopedRolePermissionQuery(params.UserID, permissionID, consts.ScopeTypeDataset, strconv.Itoa(*params.DatasetID)))
	}

	var count int64
	if err := finalQuery.Limit(1).Count(&count).Error; err != nil {
		return false, fmt.Errorf("failed to check user permission: %w", err)
	}
	return count > 0, nil
}

func (s *dbBackedMiddlewareService) buildDirectPermissionQuery(userID int, permissionID int, projectID, containerID, datasetID *int) *gorm.DB {
	query := s.db.
		Select("up.permission_id").
		Table("user_permissions up").
		Where("up.user_id = ? AND up.permission_id = ?", userID, permissionID).
		Where("up.grant_type = ?", consts.GrantTypeGrant).
		Where("up.expires_at IS NULL OR up.expires_at > ?", time.Now())

	if projectID != nil {
		query = query.Where("up.project_id IS NULL OR up.project_id = ?", *projectID)
	} else {
		query = query.Where("up.project_id IS NULL")
	}
	if containerID != nil {
		query = query.Where("up.container_id IS NULL OR up.container_id = ?", *containerID)
	} else {
		query = query.Where("up.container_id IS NULL")
	}
	if datasetID != nil {
		query = query.Where("up.dataset_id IS NULL OR up.dataset_id = ?", *datasetID)
	} else {
		query = query.Where("up.dataset_id IS NULL")
	}

	return query
}

func (s *dbBackedMiddlewareService) buildGlobalRolePermissionQuery(userID int, permissionID int) *gorm.DB {
	return s.db.
		Select("rp.permission_id").
		Table("role_permissions rp").
		Joins("JOIN user_roles ur ON rp.role_id = ur.role_id").
		Where("ur.user_id = ? AND rp.permission_id = ?", userID, permissionID)
}

// buildScopedRolePermissionQuery replaces the 4 former buildXxxRolePermissionQuery
// helpers — every scoped grant now lives in user_scoped_roles, keyed by
// (scope_type, scope_id).
func (s *dbBackedMiddlewareService) buildScopedRolePermissionQuery(userID int, permissionID int, scopeType, scopeID string) *gorm.DB {
	return s.db.
		Select("rp.permission_id").
		Table("role_permissions rp").
		Joins("JOIN user_scoped_roles usr ON rp.role_id = usr.role_id").
		Where("usr.user_id = ? AND usr.scope_type = ? AND usr.scope_id = ? AND rp.permission_id = ?",
			userID, scopeType, scopeID, permissionID).
		Where("usr.status = ?", consts.CommonEnabled)
}

func (s *dbBackedMiddlewareService) getScopedRole(userID int, scopeType, scopeID string) (*model.UserScopedRole, error) {
	var usr model.UserScopedRole
	if err := s.db.Preload("Role").
		Where("user_id = ? AND scope_type = ? AND scope_id = ? AND status = ?",
			userID, scopeType, scopeID, consts.CommonEnabled).
		First(&usr).Error; err != nil {
		return nil, err
	}
	return &usr, nil
}

func (s *dbBackedMiddlewareService) getTeamByID(teamID int) (*model.Team, error) {
	var team model.Team
	if err := s.db.Where("id = ?", teamID).First(&team).Error; err != nil {
		return nil, err
	}
	return &team, nil
}

func (s *dbBackedMiddlewareService) getResourceByName(db *gorm.DB, resourceName consts.ResourceName) (*model.Resource, error) {
	var resource model.Resource
	if err := db.Where("name = ? AND status != ?", resourceName, consts.CommonDeleted).First(&resource).Error; err != nil {
		return nil, err
	}
	return &resource, nil
}

func (s *dbBackedMiddlewareService) createAuditLog(db *gorm.DB, log *model.AuditLog) error {
	return db.Create(log).Error
}

type noopMiddlewareService struct{}

func (noopMiddlewareService) VerifyToken(context.Context, string) (*utils.Claims, error) {
	return nil, fmt.Errorf("token verifier not initialized")
}
func (noopMiddlewareService) VerifyServiceToken(context.Context, string) (*utils.ServiceClaims, error) {
	return nil, fmt.Errorf("token verifier not initialized")
}

func (noopMiddlewareService) CheckUserPermission(context.Context, *dto.CheckPermissionParams) (bool, error) {
	return false, fmt.Errorf("permission checker not initialized")
}
func (noopMiddlewareService) IsUserTeamAdmin(context.Context, int, int) (bool, error) {
	return false, fmt.Errorf("permission checker not initialized")
}
func (noopMiddlewareService) IsUserInTeam(context.Context, int, int) (bool, error) {
	return false, fmt.Errorf("permission checker not initialized")
}
func (noopMiddlewareService) IsTeamPublic(context.Context, int) (bool, error) {
	return false, fmt.Errorf("permission checker not initialized")
}
func (noopMiddlewareService) IsUserProjectAdmin(context.Context, int, int) (bool, error) {
	return false, fmt.Errorf("permission checker not initialized")
}
func (noopMiddlewareService) IsUserInProject(context.Context, int, int) (bool, error) {
	return false, fmt.Errorf("permission checker not initialized")
}

func (noopMiddlewareService) LogFailedAction(string, string, string, string, int, int, consts.ResourceName) error {
	return fmt.Errorf("audit logger not initialized")
}
func (noopMiddlewareService) LogUserAction(string, string, string, string, int, int, consts.ResourceName) error {
	return fmt.Errorf("audit logger not initialized")
}
