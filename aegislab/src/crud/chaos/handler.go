package chaos

import (
	"errors"
	"net/http"

	"aegis/platform/dto"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	Mgr *Manager
}

func NewHandler(m *Manager) *Handler { return &Handler{Mgr: m} }

// notImplemented is the §11 step-1 stand-in for endpoints whose behaviour
// arrives in later steps. ADR-0008/0010 list which surfaces those are.
func notImplemented(c *gin.Context) {
	dto.ErrorResponse(c, http.StatusNotImplemented, "endpoint not implemented in step 1")
}

type upsertSystemReq struct {
	NsPattern               string `json:"ns_pattern"                            binding:"required"`
	AppLabelKey             string `json:"app_label_key"                         binding:"required"`
	Enabled                 *bool  `json:"enabled,omitempty"`
	MaxConcurrentInjections int    `json:"max_concurrent_injections,omitempty"`
}

// PutSystem registers or updates a chaos system (target Kubernetes namespace
// + app-label key) under /v1beta/systems/{sys}.
//
//	@Summary		Register or update a chaos system
//	@Description	Upsert a chaos System binding (ns_pattern + app_label_key) under the given name.
//	@Tags			Chaos
//	@ID				chaos_put_system
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			sys		path		string										true	"System name"
//	@Param			request	body		ChaosSystemUpsertReq						true	"System upsert request"
//	@Success		200		{object}	dto.GenericResponse[ChaosSystemResp]		"System registered"
//	@Failure		400		{object}	dto.GenericResponse[any]					"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		500		{object}	dto.GenericResponse[any]					"Internal server error"
//	@Router			/v1beta/systems/{sys} [put]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) PutSystem(c *gin.Context) {
	name := c.Param("sys")
	if name == "" {
		dto.ErrorResponse(c, http.StatusBadRequest, "system name required")
		return
	}
	var req upsertSystemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	sys := &System{
		Name:                    name,
		NsPattern:               req.NsPattern,
		AppLabelKey:             req.AppLabelKey,
		Enabled:                 enabled,
		MaxConcurrentInjections: req.MaxConcurrentInjections,
	}
	if err := h.Mgr.UpsertSystem(c.Request.Context(), sys); err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	dto.SuccessResponse(c, sys)
}

// GetSystem returns the registered chaos system row.
//
//	@Summary		Get a chaos system
//	@Description	Fetch the registered chaos System by name.
//	@Tags			Chaos
//	@ID				chaos_get_system
//	@Produce		json
//	@Security		BearerAuth
//	@Param			sys	path		string									true	"System name"
//	@Success		200	{object}	dto.GenericResponse[ChaosSystemResp]	"System found"
//	@Failure		401	{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		404	{object}	dto.GenericResponse[any]				"System not found"
//	@Failure		500	{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/v1beta/systems/{sys} [get]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) GetSystem(c *gin.Context) {
	sys, err := h.Mgr.GetSystem(c.Request.Context(), c.Param("sys"))
	if err != nil {
		if errors.Is(err, ErrSystemNotFound) {
			dto.ErrorResponse(c, http.StatusNotFound, err.Error())
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	dto.SuccessResponse(c, sys)
}

// ImportPoints applies a Point manifest against a system. Pass ?dry_run=true
// to run validation in a rolled-back transaction.
//
//	@Summary		Import chaos Points from a manifest
//	@Description	POST a Point Manifest envelope to register / supersede chaos Points for a system.
//	@Tags			Chaos
//	@ID				chaos_import_points
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			sys		path		string										true	"System name"
//	@Param			dry_run	query		bool										false	"Run validation only, rollback the transaction"
//	@Param			request	body		ChaosImportPointsReq						true	"Point manifest envelope"
//	@Success		200		{object}	dto.GenericResponse[ChaosImportPointsResp]	"Import accepted (or dry-run summary)"
//	@Failure		400		{object}	dto.GenericResponse[any]					"Invalid manifest"
//	@Failure		401		{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		500		{object}	dto.GenericResponse[any]					"Internal server error"
//	@Router			/v1beta/systems/{sys}/points/import [post]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) ImportPoints(c *gin.Context) {
	sysName := c.Param("sys")
	dryRun := c.Query("dry_run") == "true"
	var m PointManifest
	if err := c.ShouldBindJSON(&m); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	res, err := h.Mgr.ImportPoints(c.Request.Context(), sysName, m, dryRun)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	dto.SuccessResponse(c, res)
}

type createInjectionReq struct {
	PointID        string         `json:"point_id"         binding:"required"`
	Params         map[string]any `json:"params"`
	IdempotencyKey string         `json:"idempotency_key"  binding:"required"`
	CallerMetadata map[string]any `json:"caller_metadata,omitempty"`
	ExecutorPin    string         `json:"executor_pin,omitempty"`
}

// CreateInjection submits a chaos injection for the given Point. The
// idempotency_key gates duplicate submissions; the response is 202 Accepted
// once the executor has acknowledged Apply.
//
//	@Summary		Submit a chaos injection
//	@Description	Create (or return the existing row for the same idempotency_key) a chaos Injection.
//	@Tags			Chaos
//	@ID				chaos_create_injection
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		ChaosCreateInjectionReq						true	"Create-injection request"
//	@Success		202		{object}	dto.GenericResponse[ChaosInjectionResp]		"Injection accepted"
//	@Failure		400		{object}	dto.GenericResponse[any]					"Invalid request / disabled system / idempotency mismatch"
//	@Failure		401		{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		404		{object}	dto.GenericResponse[any]					"Point, system or capability not found"
//	@Failure		500		{object}	dto.GenericResponse[any]					"Internal server error"
//	@Router			/v1beta/injections [post]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) CreateInjection(c *gin.Context) {
	var req createInjectionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	inj, err := h.Mgr.CreateInjection(c.Request.Context(), CreateInjectionInput{
		PointID:        req.PointID,
		Params:         req.Params,
		IdempotencyKey: req.IdempotencyKey,
		CallerMetadata: req.CallerMetadata,
		ExecutorPin:    req.ExecutorPin,
	})
	if err != nil {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, ErrSystemNotFound),
			errors.Is(err, ErrPointNotFound),
			errors.Is(err, ErrInjectionNotFound),
			errors.Is(err, ErrCapabilityNotFound):
			code = http.StatusNotFound
		case errors.Is(err, ErrSystemDisabled), errors.Is(err, ErrPointNotActive),
			errors.Is(err, ErrCapabilityUnsupported), errors.Is(err, ErrIdempotencyMismatch):
			code = http.StatusBadRequest
		}
		dto.ErrorResponse(c, code, err.Error())
		return
	}
	dto.JSONResponse(c, http.StatusAccepted, "Injection accepted", inj)
}

// GetInjection returns one Injection by id.
//
//	@Summary		Get a chaos injection
//	@Description	Fetch the persisted Injection row by id.
//	@Tags			Chaos
//	@ID				chaos_get_injection
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		string										true	"Injection id (ULID)"
//	@Success		200	{object}	dto.GenericResponse[ChaosInjectionResp]		"Injection found"
//	@Failure		401	{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		404	{object}	dto.GenericResponse[any]					"Injection not found"
//	@Router			/v1beta/injections/{id} [get]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) GetInjection(c *gin.Context) {
	inj, err := h.Mgr.GetInjection(c.Request.Context(), c.Param("id"))
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	dto.SuccessResponse(c, inj)
}

// DeleteInjection requests Destroy on the executor and moves a non-terminal
// Injection to status=cancelled. Idempotent on id.
//
//	@Summary		Destroy a chaos injection
//	@Description	Run executor Destroy and cancel a non-terminal Injection.
//	@Tags			Chaos
//	@ID				chaos_delete_injection
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		string										true	"Injection id (ULID)"
//	@Success		200	{object}	dto.GenericResponse[ChaosInjectionResp]		"Injection destroyed / cancelled"
//	@Failure		401	{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		404	{object}	dto.GenericResponse[any]					"Injection not found"
//	@Router			/v1beta/injections/{id} [delete]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) DeleteInjection(c *gin.Context) {
	inj, err := h.Mgr.DeleteInjection(c.Request.Context(), c.Param("id"))
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	dto.SuccessResponse(c, inj)
}

// ListCapabilities returns the full Capability catalog.
//
//	@Summary		List chaos capabilities
//	@Description	Return all registered Capabilities ordered by name.
//	@Tags			Chaos
//	@ID				chaos_list_capabilities
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	dto.GenericResponse[[]ChaosCapabilityResp]	"Capability catalog"
//	@Failure		401	{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		500	{object}	dto.GenericResponse[any]					"Internal server error"
//	@Router			/v1beta/capabilities [get]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) ListCapabilities(c *gin.Context) {
	out, err := h.Mgr.ListCapabilities(c.Request.Context())
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	dto.SuccessResponse(c, out)
}

// GetCapability returns one Capability by name.
//
//	@Summary		Get a chaos capability
//	@Description	Fetch one Capability entry including target/param/observable schemas.
//	@Tags			Chaos
//	@ID				chaos_get_capability
//	@Produce		json
//	@Security		BearerAuth
//	@Param			name	path		string										true	"Capability name (e.g. pod_kill)"
//	@Success		200		{object}	dto.GenericResponse[ChaosCapabilityResp]	"Capability found"
//	@Failure		401		{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		404		{object}	dto.GenericResponse[any]					"Capability not found"
//	@Router			/v1beta/capabilities/{name} [get]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) GetCapability(c *gin.Context) {
	cap, err := h.Mgr.GetCapability(c.Request.Context(), c.Param("name"))
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	dto.SuccessResponse(c, cap)
}

// ManifestSchema serves ADR-0010's "live JSON Schema for the manifest
// envelope". The schema bundled here is the offline-compatible structural
// guard — Capability target/param schemas are fetched separately by
// looking up each Capability.
//
//	@Summary		Fetch the manifest envelope JSON Schema
//	@Description	Public endpoint returning the live JSON Schema for Point Manifests (ADR-0010). Unauthenticated.
//	@Tags			Chaos
//	@ID				chaos_manifest_schema
//	@Produce		json
//	@Success		200	{object}	map[string]any	"JSON Schema document"
//	@Router			/v1beta/manifest-schema.json [get]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) ManifestSchema(c *gin.Context) {
	c.JSON(http.StatusOK, manifestEnvelopeSchema)
}

var manifestEnvelopeSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title":   "aegis-chaos PointManifest",
	"type":    "object",
	"required": []any{"apiVersion", "kind", "metadata", "spec"},
	"properties": map[string]any{
		"apiVersion": map[string]any{"const": "aegis-chaos/v1beta"},
		"kind":       map[string]any{"const": "PointManifest"},
		"metadata": map[string]any{
			"type":     "object",
			"required": []any{"system", "service", "chart_version"},
			"properties": map[string]any{
				"system":        map[string]any{"type": "string", "minLength": 1},
				"service":       map[string]any{"type": "string", "minLength": 1},
				"instance":      map[string]any{"type": "string", "default": "default"},
				"chart_version": map[string]any{"type": "string", "minLength": 1},
			},
		},
		"spec": map[string]any{
			"type":     "object",
			"required": []any{"points"},
			"properties": map[string]any{
				"replace_scope": map[string]any{
					"enum": []any{"service", "system", "none"},
				},
				"points": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []any{"capability", "target"},
						"properties": map[string]any{
							"capability":      map[string]any{"type": "string"},
							"target":          map[string]any{"type": "object"},
							"param_overrides": map[string]any{"type": "object"},
						},
					},
				},
			},
		},
	},
}
