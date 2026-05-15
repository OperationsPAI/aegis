package chaossystem

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesAdmin(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceAdmin,
		Name:     "chaossystem.admin",
		Register: func(v2 *gin.RouterGroup) {
			systems := v2.Group("/systems", middleware.TrustedHeaderAuth())
			{
				systemRead := systems.Group("", middleware.RequireSystemRead)
				{
					systemRead.GET("", handler.ListSystems)
					systemRead.GET("/:id", handler.GetSystem)
					systemRead.GET("/:id/metadata", handler.ListMetadata)
					systemRead.GET("/by-name/:name/chart", handler.GetSystemChart)
					// Prerequisites (issue #115) — read is system_read gated so
					// the default admin flow can surface status in dashboards.
					systemRead.GET("/by-name/:name/prerequisites", handler.ListPrerequisites)
					// Bulk inject-candidate enumeration (issue #181) — agent
					// loops fetch the full pool in one round-trip.
					systemRead.GET("/by-name/:name/inject-candidates", handler.ListInjectCandidates)
				}

				systemConfigure := systems.Group("", middleware.RequireSystemConfigure)
				{
					systemConfigure.POST("", handler.CreateSystem)
					systemConfigure.PUT("/:id", handler.UpdateSystem)
					systemConfigure.POST("/:id/metadata", handler.UpsertMetadata)
					systemConfigure.POST("/reseed", handler.ReseedSystems)
					// aegisctl calls this after a successful helm upgrade --install.
					systemConfigure.POST("/by-name/:name/prerequisites/:id/mark", handler.MarkPrerequisite)
				}

				systems.DELETE("/:id", middleware.RequirePermission(consts.PermSystemManage), handler.DeleteSystem)
			}
		},
	}
}
