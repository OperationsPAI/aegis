package cluster

import (
	"aegis/platform/dto"
	"aegis/platform/httpx"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service HandlerService
}

func NewHandler(service HandlerService) *Handler { return &Handler{service: service} }

// GetClusterStatus returns the aggregated per-check status grid the portal
// ClusterStatus page renders.
//
//	@Summary		Get aggregated cluster status
//	@Description	Returns one status entry per cluster preflight check (K8s API, Redis, MySQL, etcd, ClickHouse, OTel pipeline, Pedestal health, …) along with the recent-events stream. Re-runs the same catalog aegisctl cluster preflight runs, mapped into a portal-facing DTO.
//	@Tags			Cluster
//	@ID				get_cluster_status
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	dto.GenericResponse[ClusterStatusResp]	"Cluster status retrieved successfully"
//	@Failure		401	{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		500	{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/api/v2/cluster/status [get]
//	@x-api-type		{"portal":"true"}
func (h *Handler) GetClusterStatus(c *gin.Context) {
	resp, err := h.service.GetClusterStatus(c.Request.Context())
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}
