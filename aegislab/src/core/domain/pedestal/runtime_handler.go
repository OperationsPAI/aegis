package pedestal

import (
	"errors"
	"net/http"
	"strings"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RuntimeHandler is the HTTP-facing entry point for the admin /pedestals
// endpoints. It's a thin wrapper around RuntimeService — it just decodes
// JSON, maps domain errors to status codes, and serializes the response.
type RuntimeHandler struct {
	service *RuntimeService
}

func NewRuntimeHandler(service *RuntimeService) *RuntimeHandler {
	if service == nil {
		panic("pedestal.NewRuntimeHandler: service is required")
	}
	return &RuntimeHandler{service: service}
}

// InstallPedestalReq is the body for POST /api/v2/pedestals.
type InstallPedestalReq struct {
	SystemCode         string         `json:"system_code"          binding:"required"`
	ContainerVersionID int            `json:"container_version_id" binding:"required"`
	Namespace          string         `json:"namespace,omitempty"`
	HelmValues         map[string]any `json:"helm_values,omitempty"`
}

// RestartPedestalReq is the body for POST /api/v2/pedestals/:release/restart.
// All fields are optional — an empty body restarts using the
// previously-applied values.
type RestartPedestalReq struct {
	Namespace  string         `json:"namespace,omitempty"`
	HelmValues map[string]any `json:"helm_values,omitempty"`
}

// ListPedestals returns every helm release visible to the cluster, tagged
// with its system classification.
//
//	@Summary		List pedestal releases
//	@Description	Admin: enumerate every helm release across all namespaces, tagged with system classification.
//	@Tags			Pedestal
//	@ID				list_pedestal_releases
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	dto.GenericResponse[[]PedestalRelease]
//	@Router			/api/v2/pedestals [get]
//	@x-api-type		{"sdk":"true"}
func (h *RuntimeHandler) ListPedestals(c *gin.Context) {
	if _, ok := middleware.GetCurrentUserID(c); !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return
	}
	releases, err := h.service.ListReleases(c.Request.Context())
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "Failed to list pedestal releases: "+err.Error())
		return
	}
	dto.SuccessResponse(c, releases)
}

// GetPedestal returns one release's metadata + applied values.
//
//	@Summary		Get pedestal release
//	@Description	Admin: fetch one helm release plus its user-supplied values map.
//	@Tags			Pedestal
//	@ID				get_pedestal_release
//	@Produce		json
//	@Security		BearerAuth
//	@Param			release		path	string	true	"Release name"
//	@Param			namespace	query	string	false	"Namespace (defaults to release name)"
//	@Success		200	{object}	dto.GenericResponse[PedestalReleaseDetail]
//	@Failure		404	{object}	dto.GenericResponse[any]
//	@Router			/api/v2/pedestals/{release} [get]
//	@x-api-type		{"sdk":"true"}
func (h *RuntimeHandler) GetPedestal(c *gin.Context) {
	if _, ok := middleware.GetCurrentUserID(c); !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return
	}
	release := strings.TrimSpace(c.Param("release"))
	namespace := strings.TrimSpace(c.Query("namespace"))
	detail, err := h.service.GetRelease(c.Request.Context(), release, namespace)
	if err != nil {
		status, msg := mapPedestalError(err)
		dto.ErrorResponse(c, status, msg)
		return
	}
	if detail == nil {
		dto.ErrorResponse(c, http.StatusNotFound, "release not found")
		return
	}
	dto.SuccessResponse(c, detail)
}

// InstallPedestal runs a synchronous helm install.
//
//	@Summary		Install pedestal release
//	@Description	Admin: install a pedestal chart synchronously. Blocks until helm returns or the request context deadline fires.
//	@Tags			Pedestal
//	@ID				install_pedestal_release
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body	InstallPedestalReq	true	"Install request"
//	@Success		200	{object}	dto.GenericResponse[InstallPedestalResult]
//	@Failure		400	{object}	dto.GenericResponse[any]
//	@Router			/api/v2/pedestals [post]
//	@x-api-type		{"sdk":"true"}
func (h *RuntimeHandler) InstallPedestal(c *gin.Context) {
	if _, ok := middleware.GetCurrentUserID(c); !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return
	}
	var req InstallPedestalReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	result, err := h.service.Install(c.Request.Context(), InstallPedestalInput{
		SystemCode:         req.SystemCode,
		ContainerVersionID: req.ContainerVersionID,
		Namespace:          req.Namespace,
		HelmValues:         req.HelmValues,
	})
	if err != nil {
		status, msg := mapPedestalError(err)
		dto.ErrorResponse(c, status, msg)
		return
	}
	dto.SuccessResponse(c, result)
}

// RestartPedestal redeploys an existing release in place.
//
//	@Summary		Restart pedestal release
//	@Description	Admin: redeploy a pedestal release in-place. Empty body reuses the previously-applied values.
//	@Tags			Pedestal
//	@ID				restart_pedestal_release
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			release	path	string				true	"Release name"
//	@Param			request	body	RestartPedestalReq	false	"Restart request"
//	@Success		200	{object}	dto.GenericResponse[InstallPedestalResult]
//	@Router			/api/v2/pedestals/{release}/restart [post]
//	@x-api-type		{"sdk":"true"}
func (h *RuntimeHandler) RestartPedestal(c *gin.Context) {
	if _, ok := middleware.GetCurrentUserID(c); !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return
	}
	release := strings.TrimSpace(c.Param("release"))
	var req RestartPedestalReq
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
			return
		}
	}
	result, err := h.service.Restart(c.Request.Context(), release, req.Namespace, req.HelmValues)
	if err != nil {
		status, msg := mapPedestalError(err)
		dto.ErrorResponse(c, status, msg)
		return
	}
	dto.SuccessResponse(c, result)
}

// UninstallPedestal removes a release synchronously.
//
//	@Summary		Uninstall pedestal release
//	@Description	Admin: uninstall a pedestal release. Returns 204 on success; not-found is treated as success.
//	@Tags			Pedestal
//	@ID				uninstall_pedestal_release
//	@Produce		json
//	@Security		BearerAuth
//	@Param			release		path	string	true	"Release name"
//	@Param			namespace	query	string	false	"Namespace (defaults to release name)"
//	@Success		204	"No Content"
//	@Router			/api/v2/pedestals/{release} [delete]
//	@x-api-type		{"sdk":"true"}
func (h *RuntimeHandler) UninstallPedestal(c *gin.Context) {
	if _, ok := middleware.GetCurrentUserID(c); !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return
	}
	release := strings.TrimSpace(c.Param("release"))
	namespace := strings.TrimSpace(c.Query("namespace"))
	if err := h.service.Uninstall(c.Request.Context(), release, namespace, 0); err != nil {
		status, msg := mapPedestalError(err)
		dto.ErrorResponse(c, status, msg)
		return
	}
	c.Status(http.StatusNoContent)
}

// mapPedestalError translates the service-layer sentinels into HTTP status
// codes. Kept in one place so every handler in this file maps the same way.
func mapPedestalError(err error) (int, string) {
	switch {
	case errors.Is(err, consts.ErrBadRequest):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, consts.ErrNotFound):
		return http.StatusNotFound, err.Error()
	default:
		return http.StatusInternalServerError, err.Error()
	}
}
