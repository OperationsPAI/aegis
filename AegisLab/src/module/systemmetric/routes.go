package systemmetric

import (
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesAdmin(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceAdmin,
		Name:     "systemmetric.admin",
		Register: func(v2 *gin.RouterGroup) {
			system := v2.Group("/system", middleware.JWTAuth(), middleware.RequireSystemRead)
			{
				system.GET("/metrics", handler.GetSystemMetrics)
				system.GET("/metrics/history", handler.GetSystemMetricsHistory)
			}
		},
	}
}
