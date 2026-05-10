package observation

import (
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "observation-portal",
		Register: func(v2 *gin.RouterGroup) {
			injections := v2.Group("/injections", middleware.JWTAuth(), middleware.RequireProjectRead)
			{
				injections.GET("/:id/metrics/catalog", handler.GetMetricsCatalog)
				injections.GET("/:id/metrics/series", handler.GetMetricsSeries)
				injections.GET("/:id/spans", handler.ListSpans)
				injections.GET("/:id/spans/:trace_id", handler.GetSpanTree)
				injections.GET("/:id/service-map", handler.GetServiceMap)
			}
		},
	}
}
