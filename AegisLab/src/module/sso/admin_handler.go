package sso

import (
	"net/http"
	"strconv"

	"aegis/platform/dto"
	"aegis/platform/httpx"

	"github.com/gin-gonic/gin"
)

// AdminHandler exposes the `/v1/*` admin REST endpoints. Auth is via the
// existing JWTAuth middleware; per-route gating is done by requireAdminOrService
// (system admin global role OR a service token). Delegated `service_admin`
// scopes will be added in Wave 3 / Task #13.
type AdminHandler struct {
	service *AdminService
}

func NewAdminHandler(service *AdminService) *AdminHandler {
	return &AdminHandler{service: service}
}

func parseUserIDParam(c *gin.Context) (int, bool) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid user id")
		return 0, false
	}
	return id, true
}

// viewScopesFor returns the viewScopes filter for a service admin (Task #13),
// or nil for global admins / service tokens (no filtering).
func viewScopesFor(c *gin.Context) []string {
	ac := adminContextFromGin(c)
	if ac == nil || ac.IsGlobalAdmin || ac.IsServiceToken() {
		return nil
	}
	return ac.ServiceAdminFor
}

// GetUser handles GET /v1/users/{id}.
func (h *AdminHandler) GetUser(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	id, ok := parseUserIDParam(c)
	if !ok {
		return
	}
	resp, err := h.service.GetUserForAdmin(c.Request.Context(), id, viewScopesFor(c))
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// GetUsersBatch handles POST /v1/users:batch.
func (h *AdminHandler) GetUsersBatch(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	var req BatchUsersReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := h.service.GetUsersBatchForAdmin(c.Request.Context(), req.IDs, viewScopesFor(c))
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// ListUsers handles POST /v1/users:list.
func (h *AdminHandler) ListUsers(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	var req ListUsersReq
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
			return
		}
	}
	resp, err := h.service.ListUsersForAdmin(c.Request.Context(), &req, viewScopesFor(c))
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// Check handles POST /v1/check.
func (h *AdminHandler) Check(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	var req CheckReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := h.service.Check(c.Request.Context(), &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// CheckBatch handles POST /v1/check:batch.
func (h *AdminHandler) CheckBatch(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	var req BatchCheckReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if len(req.Checks) == 0 {
		dto.ErrorResponse(c, http.StatusBadRequest, "checks cannot be empty")
		return
	}
	resp, err := h.service.CheckBatch(c.Request.Context(), &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// RegisterPermissions handles POST /v1/permissions:register.
func (h *AdminHandler) RegisterPermissions(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	var req RegisterPermissionsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	ac := adminContextFromGin(c)
	resp, err := h.service.RegisterPermissionsForAdmin(c.Request.Context(), &req,
		adminScopesFor(ac), isGlobalForGate(ac))
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// Grant handles POST /v1/grants.
func (h *AdminHandler) Grant(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	var req GrantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	ac := adminContextFromGin(c)
	resp, err := h.service.GrantScopedRoleForAdmin(c.Request.Context(), &req,
		adminScopesFor(ac), isGlobalForGate(ac))
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// Revoke handles DELETE /v1/grants.
func (h *AdminHandler) Revoke(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	var req GrantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	ac := adminContextFromGin(c)
	resp, err := h.service.RevokeScopedRoleForAdmin(c.Request.Context(), &req,
		adminScopesFor(ac), isGlobalForGate(ac))
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// adminScopesFor returns the caller's service-admin scopes, or nil if global.
func adminScopesFor(ac *AdminContext) []string {
	if ac == nil || ac.IsGlobalAdmin || ac.IsServiceToken() {
		return nil
	}
	return ac.ServiceAdminFor
}

// isGlobalForGate treats global admin and service tokens as "global bypass"
// for purposes of the per-call permission gate.
func isGlobalForGate(ac *AdminContext) bool {
	if ac == nil {
		return false
	}
	return ac.IsGlobalAdmin || ac.IsServiceToken()
}

// ListUserGrants handles GET /v1/users/{id}/grants.
func (h *AdminHandler) ListUserGrants(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	id, ok := parseUserIDParam(c)
	if !ok {
		return
	}
	resp, err := h.service.ListUserGrants(c.Request.Context(), id, c.Query("scope_type"), c.Query("service"))
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// ListScopeUsers handles GET /v1/scopes/{scope_type}/{scope_id}/users.
func (h *AdminHandler) ListScopeUsers(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	scopeType := c.Param("scope_type")
	scopeID := c.Param("scope_id")
	if scopeType == "" || scopeID == "" {
		dto.ErrorResponse(c, http.StatusBadRequest, "scope_type and scope_id required")
		return
	}
	resp, err := h.service.ListScopeUsers(c.Request.Context(), scopeType, scopeID)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}
