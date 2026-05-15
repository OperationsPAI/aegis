package pedestal

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes contributes the pedestal module's portal endpoints that were
// previously registered centrally in router/portal.go.
func Routes(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "pedestal",
		Register: func(v2 *gin.RouterGroup) {
			pedestal := v2.Group("/pedestal", middleware.TrustedHeaderAuth())
			{
				helm := pedestal.Group("/helm")
				{
					helm.GET("/:container_version_id", handler.GetPedestalHelmConfig)
					helm.POST("/:container_version_id/verify", handler.VerifyPedestalHelmConfig)
					helm.PUT("/:container_version_id", middleware.RequireContainerVersionUpload, handler.UpsertPedestalHelmConfig)
					// Hot-reseed helm_configs values from data.yaml for one
					// container_version (issue #201). Same upload permission as
					// PUT — only operators with write access to container
					// versions can trigger a write reseed.
					helm.POST("/:container_version_id/reseed", middleware.RequireContainerVersionUpload, handler.ReseedPedestalHelmConfig)
				}
			}
		},
	}
}
