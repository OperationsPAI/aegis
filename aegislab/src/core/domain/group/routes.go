package group

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortal contributes the group module's portal endpoints to the
// framework route registry. These routes were previously centralized in
// router/portal.go.
func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "group",
		Register: func(v2 *gin.RouterGroup) {
			groups := v2.Group("/groups", middleware.JWTAuth(), middleware.RequireTraceRead)
			{
				groups.GET("/:group_id/stats", handler.GetGroupStats)
				groups.GET("/:group_id/stream", handler.GetGroupStream)
			}
		},
	}
}
