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

// requireAdminOrService returns true when the caller is allowed to manage
// OIDC clients: a system admin (global role) or a service token. Wave 3
// will refine this with scope-based checks (`clients:read` / `clients:write`).
func requireAdminOrService(c *gin.Context) bool {
	if v, ok := c.Get("token_type"); ok {
		if t, _ := v.(string); t == "service" {
			return true
		}
	}
	if v, ok := c.Get("is_admin"); ok {
		if a, _ := v.(bool); a {
			return true
		}
	}
	dto.ErrorResponse(c, http.StatusForbidden, "Forbidden: requires system admin or service token")
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

func (h *Handler) Create(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	var req CreateClientReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
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
	clients, err := h.service.List(c.Request.Context(), c.Query("service"))
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
