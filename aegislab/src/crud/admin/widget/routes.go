package widget

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

func Routes(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceAdmin,
		Name:     "widget",
		Register: func(v2 *gin.RouterGroup) {
			widgets := v2.Group("/widgets", middleware.TrustedHeaderAuth())
			widgets.GET("/ping", middleware.RequirePermission(PermWidgetReadAll), handler.GetPing)
		},
	}
}
