package trace

import (
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "trace",
		Register: func(v2 *gin.RouterGroup) {
			traces := v2.Group("/traces", middleware.JWTAuth(), middleware.RequireTraceRead)
			{
				traces.GET("", handler.ListTraces)
				traces.GET("/:trace_id", handler.GetTrace)
				traces.GET("/:trace_id/stream", handler.GetTraceStream)
			}
		},
	}
}
