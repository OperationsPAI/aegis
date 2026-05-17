package trace

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "trace",
		Register: func(v2 *gin.RouterGroup) {
			traces := v2.Group("/traces", middleware.TrustedHeaderAuth(), middleware.RequireTraceRead)
			{
				traces.GET("", handler.ListTraces)
				traces.GET("/:trace_id", handler.GetTrace)
				traces.GET("/:trace_id/spans", handler.GetTraceSpans)
				traces.GET("/:trace_id/stream", handler.GetTraceStream)
			}
			tracesWrite := v2.Group("/traces", middleware.TrustedHeaderAuth(), middleware.RequireTraceWrite)
			{
				tracesWrite.POST("/:trace_id/cancel", handler.CancelTrace)
			}
		},
	}
}
