package systemmetric

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesAdmin contributes the real-time system metric endpoints to the
// admin audience. These routes were introduced directly in the module as
// part of the Phase 4 migration, so there is no remaining centralized
// router block to delete for this module.
func RoutesAdmin(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceAdmin,
		Name:     "systemmetric.admin",
		Register: func(v2 *gin.RouterGroup) {
			system := v2.Group("/system", middleware.TrustedHeaderAuth(), middleware.RequireSystemRead)
			{
				system.GET("/metrics", handler.GetSystemMetrics)
				system.GET("/metrics/history", handler.GetSystemMetricsHistory)
			}
		},
	}
}
