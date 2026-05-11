package sso

import (
	"errors"
	"net/http"
	"strconv"

	"aegis/consts"
	"aegis/dto"
	"aegis/httpx"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// AdminContext carries authorization info for the /v1/* SSO admin surface
// (Task #13). Exactly one of IsGlobalAdmin / len(ServiceAdminFor)>0 /
// ServiceTokenFor!="" must hold for the caller to reach a handler at all;
// handlers consult the non-bypass fields to filter responses to the caller's
// admin services.
type AdminContext struct {
	// IsGlobalAdmin true when the caller has the system_admin/super_admin
	// global role. Global admins bypass all service-scope filters.
	IsGlobalAdmin bool
	// ServiceAdminFor lists the downstream service names the caller is
	// service-admin of (via user_scoped_roles with scope_type=ScopeTypeService).
	// Empty iff the caller is not a service admin.
	ServiceAdminFor []string
	// ServiceTokenFor is set when the caller authenticated via a service /
	// k8s task token. Currently service tokens grant full access (matches
	// pre-existing gate behavior) so this is primarily informational.
	ServiceTokenFor string
}

// IsServiceToken reports whether the caller authenticated via a service token.
func (a *AdminContext) IsServiceToken() bool {
	return a != nil && a.ServiceTokenFor != ""
}

// IsServiceAdmin reports whether the caller has at least one service-admin grant.
func (a *AdminContext) IsServiceAdmin() bool {
	return a != nil && len(a.ServiceAdminFor) > 0
}

// MayActOnService reports whether the caller is authorized for the given
// downstream service. Global admin / service token always wins; service
// admins must have the service in their admin set.
func (a *AdminContext) MayActOnService(service string) bool {
	if a == nil {
		return false
	}
	if a.IsGlobalAdmin || a.IsServiceToken() {
		return true
	}
	for _, s := range a.ServiceAdminFor {
		if s == service {
			return true
		}
	}
	return false
}

const adminContextKey = "sso_admin_context"

func adminContextFromGin(c *gin.Context) *AdminContext {
	if v, ok := c.Get(adminContextKey); ok {
		if ctx, _ := v.(*AdminContext); ctx != nil {
			return ctx
		}
	}
	return nil
}

// AdminScopeResolver looks up a user's service-admin scopes. The /v1 gate
// uses it to enrich an AdminContext for non-global-admin user tokens. Kept
// as a narrow interface so handler.go doesn't depend on rbac directly.
type AdminScopeResolver interface {
	ListServiceAdminScopes(userID int) ([]string, error)
}

var scopeResolver AdminScopeResolver

// SetAdminScopeResolver registers the resolver used by requireAdminOrService.
// Wired from module.go at fx startup.
func SetAdminScopeResolver(r AdminScopeResolver) { scopeResolver = r }

// requireAdminOrService is the per-route gate. Allows the request when the
// caller is a service token, a global admin, or a service admin for at least
// one downstream service. The resolved AdminContext is stashed on the gin
// context for handlers to consult when filtering responses.
func requireAdminOrService(c *gin.Context) bool {
	ctx := &AdminContext{}
	if v, ok := c.Get("token_type"); ok {
		if t, _ := v.(string); t == "service" {
			ctx.ServiceTokenFor = "service"
			if sv, ok := c.Get("service"); ok {
				if s, _ := sv.(string); s != "" {
					ctx.ServiceTokenFor = s
				}
			}
		}
	}
	if v, ok := c.Get("is_admin"); ok {
		if a, _ := v.(bool); a {
			ctx.IsGlobalAdmin = true
		}
	}
	if !ctx.IsGlobalAdmin && !ctx.IsServiceToken() && scopeResolver != nil {
		if v, ok := c.Get("user_id"); ok {
			if uid, _ := v.(int); uid > 0 {
				scopes, err := scopeResolver.ListServiceAdminScopes(uid)
				if err == nil {
					ctx.ServiceAdminFor = scopes
				}
			}
		}
	}

	if ctx.IsGlobalAdmin || ctx.IsServiceToken() || ctx.IsServiceAdmin() {
		c.Set(adminContextKey, ctx)
		return true
	}
	dto.ErrorResponse(c, http.StatusForbidden, "Forbidden: requires system admin, service admin, or service token")
	c.Abort()
	return false
}

func parseClientID(c *gin.Context) (int, bool) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid client id")
		return 0, false
	}
	return id, true
}

// checkClientOwnership 403s when the caller is a service admin and the
// targeted OIDC client belongs to a service outside their admin set.
// Returns false when the response has already been written (handler should
// stop). Global admins / service tokens are waved through.
func (h *Handler) checkClientOwnership(c *gin.Context, id int) bool {
	ac := adminContextFromGin(c)
	if ac == nil || ac.IsGlobalAdmin || ac.IsServiceToken() {
		return true
	}
	cli, err := h.service.Get(c.Request.Context(), id)
	if errors.Is(err, consts.ErrNotFound) {
		dto.ErrorResponse(c, http.StatusNotFound, "Client not found")
		return false
	}
	if httpx.HandleServiceError(c, err) {
		return false
	}
	if !ac.MayActOnService(cli.Service) {
		dto.ErrorResponse(c, http.StatusForbidden, "Forbidden: not service admin for "+cli.Service)
		return false
	}
	return true
}

func (h *Handler) Create(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	var req CreateClientReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if ac := adminContextFromGin(c); ac != nil && !ac.MayActOnService(req.Service) {
		dto.ErrorResponse(c, http.StatusForbidden, "Forbidden: not service admin for "+req.Service)
		return
	}
	resp, err := h.service.Create(c.Request.Context(), &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.JSONResponse(c, http.StatusCreated, "Client created successfully", resp)
}

func (h *Handler) List(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	serviceFilter := c.Query("service")
	ac := adminContextFromGin(c)
	if ac != nil && !ac.IsGlobalAdmin && !ac.IsServiceToken() {
		if serviceFilter != "" && !ac.MayActOnService(serviceFilter) {
			dto.ErrorResponse(c, http.StatusForbidden, "Forbidden: not service admin for "+serviceFilter)
			return
		}
		if serviceFilter == "" {
			clients, err := h.service.ListForServices(c.Request.Context(), ac.ServiceAdminFor)
			if httpx.HandleServiceError(c, err) {
				return
			}
			dto.SuccessResponse(c, clients)
			return
		}
	}
	clients, err := h.service.List(c.Request.Context(), serviceFilter)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, clients)
}

func (h *Handler) Get(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	id, ok := parseClientID(c)
	if !ok {
		return
	}
	resp, err := h.service.Get(c.Request.Context(), id)
	if errors.Is(err, consts.ErrNotFound) {
		dto.ErrorResponse(c, http.StatusNotFound, "Client not found")
		return
	}
	if httpx.HandleServiceError(c, err) {
		return
	}
	if ac := adminContextFromGin(c); ac != nil && resp != nil && !ac.MayActOnService(resp.Service) {
		dto.ErrorResponse(c, http.StatusForbidden, "Forbidden: not service admin for "+resp.Service)
		return
	}
	dto.SuccessResponse(c, resp)
}

func (h *Handler) Update(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	id, ok := parseClientID(c)
	if !ok {
		return
	}
	if !h.checkClientOwnership(c, id) {
		return
	}
	var req UpdateClientReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	resp, err := h.service.Update(c.Request.Context(), id, &req)
	if errors.Is(err, consts.ErrNotFound) {
		dto.ErrorResponse(c, http.StatusNotFound, "Client not found")
		return
	}
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.JSONResponse(c, http.StatusOK, "Client updated successfully", resp)
}

func (h *Handler) Rotate(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	id, ok := parseClientID(c)
	if !ok {
		return
	}
	if !h.checkClientOwnership(c, id) {
		return
	}
	resp, err := h.service.RotateSecret(c.Request.Context(), id)
	if errors.Is(err, consts.ErrNotFound) {
		dto.ErrorResponse(c, http.StatusNotFound, "Client not found")
		return
	}
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.JSONResponse(c, http.StatusOK, "Client secret rotated", resp)
}

func (h *Handler) Delete(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	id, ok := parseClientID(c)
	if !ok {
		return
	}
	if !h.checkClientOwnership(c, id) {
		return
	}
	err := h.service.Delete(c.Request.Context(), id)
	if errors.Is(err, consts.ErrNotFound) {
		dto.ErrorResponse(c, http.StatusNotFound, "Client not found")
		return
	}
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.JSONResponse(c, http.StatusOK, "Client deleted", gin.H{"id": id})
}
