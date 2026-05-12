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

// PermissionChecker is the narrow interface middleware needs from SSO. The
// concrete *ssoclient.Client is adapted to this in module/ssoclient so that
// middleware does not depend on the ssoclient package (which would close an
// import cycle — ssoclient imports middleware for TokenVerifier).
type PermissionChecker interface {
	Check(ctx context.Context, userID int, permission, scopeType, scopeID string) (bool, error)
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

// NewService constructs the request-time middleware service.
//
// Token verification and RBAC permission checks are delegated to sso via
// the provided verifier and checker. The *gorm.DB is retained for two
// AegisLab-owned reads/writes that are not SSO concerns: the audit_logs table
// and the teams table (used by IsTeamPublic to enforce public-team access).
func NewService(verifier TokenVerifier, checker PermissionChecker, db *gorm.DB) Service {
	return &ssoBackedMiddlewareService{verifier: verifier, checker: checker, db: db}
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

// AuditLogger is the exported alias of the package-internal auditLogger interface,
// so handler packages can take a dependency on the audit surface without re-declaring it.
type AuditLogger = auditLogger

// AuditLoggerFromContext returns the audit logger attached to the gin context, or a
// no-op stub if the middleware service was not injected. Always safe to call.
func AuditLoggerFromContext(c *gin.Context) AuditLogger { return auditLoggerFromContext(c) }

// AuditAction emits an audit log row for the current request. On non-nil err it
// records a failed action; otherwise a successful one. Errors from the audit
// write itself are intentionally swallowed — audit logging is best-effort and
// must never block the handler's response.
func AuditAction(c *gin.Context, action, details string, err error, start time.Time, userID int, resource consts.ResourceName) {
	auditor := AuditLoggerFromContext(c)
	if auditor == nil {
		return
	}
	duration := int(time.Since(start).Milliseconds())
	ip := c.ClientIP()
	ua := c.Request.UserAgent()
	if err != nil {
		_ = auditor.LogFailedAction(ip, ua, action, err.Error(), duration, userID, resource)
		return
	}
	_ = auditor.LogUserAction(ip, ua, action, details, duration, userID, resource)
}

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

type ssoBackedMiddlewareService struct {
	verifier TokenVerifier
	checker  PermissionChecker
	db       *gorm.DB
}

func (s *ssoBackedMiddlewareService) VerifyToken(ctx context.Context, token string) (*utils.Claims, error) {
	if s.verifier == nil {
		return nil, fmt.Errorf("token verifier not initialized")
	}
	return s.verifier.VerifyToken(ctx, token)
}

func (s *ssoBackedMiddlewareService) VerifyServiceToken(ctx context.Context, token string) (*utils.ServiceClaims, error) {
	if s.verifier == nil {
		return nil, fmt.Errorf("token verifier not initialized")
	}
	return s.verifier.VerifyServiceToken(ctx, token)
}

func (s *ssoBackedMiddlewareService) CheckUserPermission(ctx context.Context, params *dto.CheckPermissionParams) (bool, error) {
	if err := params.Validate(); err != nil {
		return false, fmt.Errorf("invalid request: %w", err)
	}
	if s.checker == nil {
		return false, fmt.Errorf("permission checker not initialized")
	}

	rule := consts.PermissionRule{Resource: params.ResourceName, Action: params.Action, Scope: params.Scope}
	scopeType, scopeID := pickScope(params)
	return s.checker.Check(ctx, params.UserID, rule.String(), scopeType, scopeID)
}

// pickScope mirrors the priority the SQL UNION used to encode: the most
// specific ID present on the request wins. Callers only ever set one of
// these in practice; the explicit order keeps behavior stable if more than
// one is passed.
func pickScope(params *dto.CheckPermissionParams) (scopeType, scopeID string) {
	switch {
	case params.TeamID != nil:
		return consts.ScopeTypeTeam, strconv.Itoa(*params.TeamID)
	case params.ProjectID != nil:
		return consts.ScopeTypeProject, strconv.Itoa(*params.ProjectID)
	case params.ContainerID != nil:
		return consts.ScopeTypeContainer, strconv.Itoa(*params.ContainerID)
	case params.DatasetID != nil:
		return consts.ScopeTypeDataset, strconv.Itoa(*params.DatasetID)
	}
	return "", ""
}

func (s *ssoBackedMiddlewareService) checkScoped(ctx context.Context, userID int, perm consts.PermissionRule, scopeType, scopeID string) (bool, error) {
	if s.checker == nil {
		return false, fmt.Errorf("permission checker not initialized")
	}
	return s.checker.Check(ctx, userID, perm.String(), scopeType, scopeID)
}

func (s *ssoBackedMiddlewareService) IsUserInTeam(ctx context.Context, userID, teamID int) (bool, error) {
	return s.checkScoped(ctx, userID, consts.PermTeamReadTeam, consts.ScopeTypeTeam, strconv.Itoa(teamID))
}

func (s *ssoBackedMiddlewareService) IsUserTeamAdmin(ctx context.Context, userID, teamID int) (bool, error) {
	return s.checkScoped(ctx, userID, consts.PermTeamManageAll, consts.ScopeTypeTeam, strconv.Itoa(teamID))
}

func (s *ssoBackedMiddlewareService) IsUserInProject(ctx context.Context, userID, projectID int) (bool, error) {
	return s.checkScoped(ctx, userID, consts.PermProjectReadOwn, consts.ScopeTypeProject, strconv.Itoa(projectID))
}

func (s *ssoBackedMiddlewareService) IsUserProjectAdmin(ctx context.Context, userID, projectID int) (bool, error) {
	return s.checkScoped(ctx, userID, consts.PermProjectManageOwn, consts.ScopeTypeProject, strconv.Itoa(projectID))
}

// IsTeamPublic stays on the local DB: team.is_public is AegisLab business
// data, not an SSO permission.
func (s *ssoBackedMiddlewareService) IsTeamPublic(_ context.Context, teamID int) (bool, error) {
	if s.db == nil {
		return false, nil
	}
	team, err := s.getTeamByID(teamID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return team.IsPublic, nil
}

func (s *ssoBackedMiddlewareService) LogFailedAction(ipAddress, userAgent, action, errorMsg string, duration, userID int, resourceName consts.ResourceName) error {
	if resourceName == "" {
		return fmt.Errorf("resource name cannot be empty")
	}
	if s.db == nil {
		return fmt.Errorf("audit logger: db not initialized")
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

func (s *ssoBackedMiddlewareService) LogUserAction(ipAddress, userAgent, action, details string, duration, userID int, resourceName consts.ResourceName) error {
	if resourceName == "" {
		return fmt.Errorf("resource name cannot be empty")
	}
	if s.db == nil {
		return fmt.Errorf("audit logger: db not initialized")
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

func (s *ssoBackedMiddlewareService) getTeamByID(teamID int) (*model.Team, error) {
	var team model.Team
	if err := s.db.Where("id = ?", teamID).First(&team).Error; err != nil {
		return nil, err
	}
	return &team, nil
}

func (s *ssoBackedMiddlewareService) getResourceByName(db *gorm.DB, resourceName consts.ResourceName) (*model.Resource, error) {
	var resource model.Resource
	if err := db.Where("name = ? AND status != ?", resourceName, consts.CommonDeleted).First(&resource).Error; err != nil {
		return nil, err
	}
	return &resource, nil
}

func (s *ssoBackedMiddlewareService) createAuditLog(db *gorm.DB, log *model.AuditLog) error {
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
