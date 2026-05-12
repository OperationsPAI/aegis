package widget

import (
	"aegis/platform/dto"
	"aegis/platform/httpx"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service HandlerService
}

func NewHandler(service HandlerService) *Handler {
	return &Handler{service: service}
}

func (h *Handler) GetPing(c *gin.Context) {
	resp, err := h.service.Ping(c.Request.Context())
	if err != nil {
		httpx.HandleServiceError(c, err)
		return
	}
	dto.JSONResponse(c, http.StatusOK, "Widget self-registration is active", resp)
}
