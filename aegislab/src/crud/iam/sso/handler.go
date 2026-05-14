package sso

import (
	"errors"
	"net/http"
	"strconv"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/httpx"

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
	if v, ok := c.Get(consts.CtxKeyTokenType); ok {
		if t, _ := v.(string); t == "service" {
			ctx.ServiceTokenFor = "service"
			if sv, ok := c.Get("service"); ok {
				if s, _ := sv.(string); s != "" {
					ctx.ServiceTokenFor = s
				}
			}
		}
	}
	if v, ok := c.Get(consts.CtxKeyIsAdmin); ok {
		if a, _ := v.(bool); a {
			ctx.IsGlobalAdmin = true
		}
	}
	if !ctx.IsGlobalAdmin && !ctx.IsServiceToken() && scopeResolver != nil {
		if v, ok := c.Get(consts.CtxKeyUserID); ok {
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

// Create registers a new OIDC client.
//
//	@Summary		Create OIDC client
//	@Description	Register a new OIDC client for a downstream service. Returns the generated `client_secret` only on creation; callers must persist it immediately.
//	@Tags			SSO Clients
//	@ID				sso_create_client
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		CreateClientReq							true	"OIDC client registration"
//	@Success		201		{object}	dto.GenericResponse[CreateClientResp]	"Client created successfully"
//	@Failure		400		{object}	dto.GenericResponse[any]				"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]				"Forbidden: not service admin for the target service"
//	@Failure		500		{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/v1/clients [post]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// List returns OIDC clients visible to the caller.
//
//	@Summary		List OIDC clients
//	@Description	List OIDC clients. Service admins see only clients for their admin services; global admins / service tokens see all. Optional `service` filter narrows the result.
//	@Tags			SSO Clients
//	@ID				sso_list_clients
//	@Produce		json
//	@Security		BearerAuth
//	@Param			service	query		string								false	"Filter by downstream service name"
//	@Success		200		{object}	dto.GenericResponse[[]ClientResp]	"Clients listed successfully"
//	@Failure		401		{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]			"Forbidden"
//	@Failure		500		{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/v1/clients [get]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// Get returns a single OIDC client by id.
//
//	@Summary		Get OIDC client
//	@Description	Get an OIDC client by record id. Service admins are restricted to clients for their admin services.
//	@Tags			SSO Clients
//	@ID				sso_get_client
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		int								true	"Client record id"
//	@Success		200	{object}	dto.GenericResponse[ClientResp]	"Client retrieved successfully"
//	@Failure		400	{object}	dto.GenericResponse[any]		"Invalid client id"
//	@Failure		401	{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		403	{object}	dto.GenericResponse[any]		"Forbidden"
//	@Failure		404	{object}	dto.GenericResponse[any]		"Client not found"
//	@Failure		500	{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/v1/clients/{id} [get]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// Update modifies an existing OIDC client.
//
//	@Summary		Update OIDC client
//	@Description	Update mutable fields (name, redirect_uris, grants, scopes) of an OIDC client.
//	@Tags			SSO Clients
//	@ID				sso_update_client
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		int								true	"Client record id"
//	@Param			request	body		UpdateClientReq					true	"OIDC client update"
//	@Success		200		{object}	dto.GenericResponse[ClientResp]	"Client updated successfully"
//	@Failure		400		{object}	dto.GenericResponse[any]		"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]		"Forbidden"
//	@Failure		404		{object}	dto.GenericResponse[any]		"Client not found"
//	@Failure		500		{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/v1/clients/{id} [put]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// Rotate generates a new client secret for an OIDC client.
//
//	@Summary		Rotate OIDC client secret
//	@Description	Rotate the `client_secret` for an OIDC client. The new plaintext secret is returned only once and must be persisted by the caller immediately.
//	@Tags			SSO Clients
//	@ID				sso_rotate_client_secret
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		int										true	"Client record id"
//	@Success		200	{object}	dto.GenericResponse[RotateSecretResp]	"Client secret rotated"
//	@Failure		400	{object}	dto.GenericResponse[any]				"Invalid client id"
//	@Failure		401	{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		403	{object}	dto.GenericResponse[any]				"Forbidden"
//	@Failure		404	{object}	dto.GenericResponse[any]				"Client not found"
//	@Failure		500	{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/v1/clients/{id}/rotate [post]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// Delete removes an OIDC client.
//
//	@Summary		Delete OIDC client
//	@Description	Delete an OIDC client. Existing tokens issued for the client are not retroactively revoked.
//	@Tags			SSO Clients
//	@ID				sso_delete_client
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		int							true	"Client record id"
//	@Success		200	{object}	dto.GenericResponse[any]	"Client deleted"
//	@Failure		400	{object}	dto.GenericResponse[any]	"Invalid client id"
//	@Failure		401	{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		403	{object}	dto.GenericResponse[any]	"Forbidden"
//	@Failure		404	{object}	dto.GenericResponse[any]	"Client not found"
//	@Failure		500	{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/v1/clients/{id} [delete]
//	@x-api-type		{"portal":"true","admin":"true"}
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
