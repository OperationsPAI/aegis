package sso

import (
	"net/http"
	"strconv"

	"aegis/dto"
	"aegis/httpx"

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

// GetUser handles GET /v1/users/{id}.
func (h *AdminHandler) GetUser(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	id, ok := parseUserIDParam(c)
	if !ok {
		return
	}
	resp, err := h.service.GetUser(c.Request.Context(), id)
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
	resp, err := h.service.GetUsersBatch(c.Request.Context(), req.IDs)
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
	resp, err := h.service.ListUsers(c.Request.Context(), &req)
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
	resp, err := h.service.RegisterPermissions(c.Request.Context(), &req)
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
	resp, err := h.service.GrantScopedRole(c.Request.Context(), &req)
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
	resp, err := h.service.RevokeScopedRole(c.Request.Context(), &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
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
