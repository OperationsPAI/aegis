package dashboard

import (
	"net/http"
	"strconv"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/httpx"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service HandlerService
}

func NewHandler(service HandlerService) *Handler {
	return &Handler{service: service}
}

// GetProjectDashboard returns a fan-in aggregate for the portal dashboard page.
//
//	@Summary		Get project dashboard aggregate
//	@Description	Returns the KPI tiles and recent-activity panels for a project in a single round-trip. Combines project metadata, point-in-time totals (injections, executions, running tasks, traces), and the 10 most recent injections, executions, and traces — ordered most-recent-first.
//	@Tags			Projects
//	@ID				get_project_dashboard
//	@Produce		json
//	@Security		BearerAuth
//	@Param			project_id	path		int									true	"Project ID"
//	@Success		200			{object}	dto.GenericResponse[DashboardResp]	"Dashboard aggregate retrieved successfully"
//	@Failure		400			{object}	dto.GenericResponse[any]			"Invalid project ID"
//	@Failure		401			{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		403			{object}	dto.GenericResponse[any]			"Permission denied"
//	@Failure		404			{object}	dto.GenericResponse[any]			"Project not found"
//	@Failure		500			{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/api/v2/projects/{project_id}/dashboard [get]
//	@x-api-type		{"portal":"true"}
func (h *Handler) GetProjectDashboard(c *gin.Context) {
	projectIDStr := c.Param(consts.URLPathProjectID)
	projectID, err := strconv.Atoi(projectIDStr)
	if err != nil || projectID <= 0 {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid project ID")
		return
	}

	resp, err := h.service.GetProjectDashboard(c.Request.Context(), projectID)
	if httpx.HandleServiceError(c, err) {
		return
	}

	dto.SuccessResponse(c, resp)
}
