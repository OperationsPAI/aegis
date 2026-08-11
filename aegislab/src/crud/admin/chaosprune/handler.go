package chaosprune

import (
	"fmt"
	"net/http"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/httpx"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

type Handler struct {
	service HandlerService
}

func NewHandler(service HandlerService) *Handler {
	return &Handler{service: service}
}

// Prune
//
//	@Summary		Prune orphaned chaos-mesh CRs
//	@Description	Find and (optionally) delete chaos-mesh CRs whose backing
//	@Description	injection task is in a terminal state or no longer exists.
//	@Description	Defaults to dry-run; pass `"dry_run": false` to actually
//	@Description	delete. Admin-only.
//	@Tags			ChaosPrune
//	@ID				prune_chaos
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			req	body		PruneReq	true	"Prune parameters"
//	@Success		200	{object}	dto.GenericResponse[PruneResp]
//	@Router			/api/v2/admin/chaos/prune [post]
func (h *Handler) Prune(c *gin.Context) {
	var req PruneReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, fmt.Sprintf("invalid body: %v", err))
		return
	}
	resp, err := h.service.Prune(c.Request.Context(), &req)
	if err != nil {
		logrus.WithError(err).Error("chaos prune failed")
		httpx.HandleServiceError(c, fmt.Errorf("%w: %v", consts.ErrInternal, err))
		return
	}
	dto.JSONResponse(c, http.StatusOK, "Chaos prune complete", resp)
}
