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

// GetPing reports whether the widget module's self-registration is active
//
//	@Summary		Widget self-registration ping
//	@Description	Probe endpoint that confirms the widget module is mounted via framework self-registration
//	@Tags			Widget
//	@ID				get_widget_ping
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	dto.GenericResponse[PingResp]	"Widget self-registration is active"
//	@Failure		401	{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		403	{object}	dto.GenericResponse[any]		"Permission denied"
//	@Failure		500	{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/api/v2/widgets/ping [get]
func (h *Handler) GetPing(c *gin.Context) {
	resp, err := h.service.Ping(c.Request.Context())
	if err != nil {
		httpx.HandleServiceError(c, err)
		return
	}
	dto.JSONResponse(c, http.StatusOK, "Widget self-registration is active", resp)
}
