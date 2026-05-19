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
			widgets := v2.Group("/widgets", middleware.RequireSystemAdmin())
			widgets.GET("/ping", handler.GetPing)
		},
	}
}
