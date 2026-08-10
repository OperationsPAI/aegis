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

// GetUser returns a single user by id for SSO admin callers.
//
//	@Summary		Get user (SSO admin)
//	@Description	Get a single user's SSO-facing projection. Service admins see only users granted into their admin services.
//	@Tags			SSO Admin
//	@ID				sso_admin_get_user
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		int									true	"User id"
//	@Success		200	{object}	dto.GenericResponse[UserInfoResp]	"User retrieved successfully"
//	@Failure		400	{object}	dto.GenericResponse[any]			"Invalid user id"
//	@Failure		401	{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		403	{object}	dto.GenericResponse[any]			"Forbidden"
//	@Failure		404	{object}	dto.GenericResponse[any]			"User not found"
//	@Failure		500	{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/v1/users/{id} [get]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// GetUsersBatch returns multiple users by id in one call.
//
//	@Summary		Batch get users (SSO admin)
//	@Description	Resolve up to 1000 user ids to their SSO-facing projection in a single round trip.
//	@Tags			SSO Admin
//	@ID				sso_admin_get_users_batch
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		BatchUsersReq						true	"User ids to resolve"
//	@Success		200		{object}	dto.GenericResponse[[]UserInfoResp]	"Users retrieved successfully"
//	@Failure		400		{object}	dto.GenericResponse[any]			"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]			"Forbidden"
//	@Failure		500		{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/v1/users/batch [post]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// ListUsers returns a paginated list of users for SSO admin callers.
//
//	@Summary		List users (SSO admin)
//	@Description	List users with optional `is_active` / `status` filters. Service admins are restricted to users granted into their admin services.
//	@Tags			SSO Admin
//	@ID				sso_admin_list_users
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		ListUsersReq						false	"Pagination and filter options"
//	@Success		200		{object}	dto.GenericResponse[ListUsersResp]	"Users listed successfully"
//	@Failure		400		{object}	dto.GenericResponse[any]			"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]			"Forbidden"
//	@Failure		500		{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/v1/users/list [post]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// Check evaluates a single permission for a user.
//
//	@Summary		Check permission (SSO admin)
//	@Description	Evaluate whether a user holds a permission, optionally narrowed to a `(scope_type, scope_id)` pair.
//	@Tags			SSO Admin
//	@ID				sso_admin_check
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		CheckReq						true	"Permission check"
//	@Success		200		{object}	dto.GenericResponse[CheckResp]	"Check completed"
//	@Failure		400		{object}	dto.GenericResponse[any]		"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]		"Forbidden"
//	@Failure		500		{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/v1/check [post]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// CheckBatch evaluates multiple permission checks in one call.
//
//	@Summary		Batch check permissions (SSO admin)
//	@Description	Evaluate multiple `(user_id, permission, scope)` checks in a single round trip.
//	@Tags			SSO Admin
//	@ID				sso_admin_check_batch
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		BatchCheckReq						true	"Batch of permission checks"
//	@Success		200		{object}	dto.GenericResponse[[]CheckResp]	"Checks completed"
//	@Failure		400		{object}	dto.GenericResponse[any]			"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]			"Forbidden"
//	@Failure		500		{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/v1/check/batch [post]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// RegisterPermissions upserts a batch of permissions for a service.
//
//	@Summary		Register permissions (SSO admin)
//	@Description	Upsert a batch of permission definitions for a downstream service. Service admins may only register permissions for services they administer.
//	@Tags			SSO Admin
//	@ID				sso_admin_register_permissions
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		RegisterPermissionsReq						true	"Service permissions to register"
//	@Success		200		{object}	dto.GenericResponse[RegisterPermissionsResp]	"Permissions registered"
//	@Failure		400		{object}	dto.GenericResponse[any]					"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]					"Forbidden"
//	@Failure		500		{object}	dto.GenericResponse[any]					"Internal server error"
//	@Router			/v1/permissions/register [post]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// Grant assigns a scoped role to a user.
//
//	@Summary		Grant scoped role (SSO admin)
//	@Description	Assign a role to a user within a `(scope_type, scope_id)` pair. Role can be referenced by `role_id` or `role` name.
//	@Tags			SSO Admin
//	@ID				sso_admin_grant
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		GrantReq						true	"Grant request"
//	@Success		200		{object}	dto.GenericResponse[GrantResp]	"Grant created"
//	@Failure		400		{object}	dto.GenericResponse[any]		"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]		"Forbidden"
//	@Failure		500		{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/v1/grants [post]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// Revoke removes a scoped role assignment from a user.
//
//	@Summary		Revoke scoped role (SSO admin)
//	@Description	Remove a previously granted scoped role from a user.
//	@Tags			SSO Admin
//	@ID				sso_admin_revoke
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		GrantReq						true	"Revoke request"
//	@Success		200		{object}	dto.GenericResponse[RevokeResp]	"Grant revoked"
//	@Failure		400		{object}	dto.GenericResponse[any]		"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]		"Forbidden"
//	@Failure		500		{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/v1/grants [delete]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// ListUserGrants returns the scoped role assignments for a user.
//
//	@Summary		List user grants (SSO admin)
//	@Description	List all scoped role assignments held by a user, optionally filtered by `scope_type` and `service`.
//	@Tags			SSO Admin
//	@ID				sso_admin_list_user_grants
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id			path		int										true	"User id"
//	@Param			scope_type	query		string									false	"Filter by scope type"
//	@Param			service		query		string									false	"Filter by service name"
//	@Success		200			{object}	dto.GenericResponse[[]UserGrantResp]	"Grants listed successfully"
//	@Failure		400			{object}	dto.GenericResponse[any]				"Invalid user id"
//	@Failure		401			{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		403			{object}	dto.GenericResponse[any]				"Forbidden"
//	@Failure		500			{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/v1/users/{id}/grants [get]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// ListScopeUsers returns members of a scope with their role.
//
//	@Summary		List scope members (SSO admin)
//	@Description	List users granted into a `(scope_type, scope_id)` pair, with their assigned role.
//	@Tags			SSO Admin
//	@ID				sso_admin_list_scope_users
//	@Produce		json
//	@Security		BearerAuth
//	@Param			scope_type	path		string									true	"Scope type"
//	@Param			scope_id	path		string									true	"Scope id"
//	@Success		200			{object}	dto.GenericResponse[[]ScopeUserResp]	"Members listed successfully"
//	@Failure		400			{object}	dto.GenericResponse[any]				"Invalid scope"
//	@Failure		401			{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		403			{object}	dto.GenericResponse[any]				"Forbidden"
//	@Failure		500			{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/v1/scopes/{scope_type}/{scope_id}/users [get]
//	@x-api-type		{"portal":"true","admin":"true"}
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
