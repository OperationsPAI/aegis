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

func (h *Handler) GetInjection(c *gin.Context) {
	inj, err := h.Mgr.GetInjection(c.Request.Context(), c.Param("id"))
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	dto.SuccessResponse(c, inj)
}

func (h *Handler) DeleteInjection(c *gin.Context) {
	inj, err := h.Mgr.DeleteInjection(c.Request.Context(), c.Param("id"))
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	dto.SuccessResponse(c, inj)
}

func (h *Handler) ListCapabilities(c *gin.Context) {
	out, err := h.Mgr.ListCapabilities(c.Request.Context())
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	dto.SuccessResponse(c, out)
}

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
