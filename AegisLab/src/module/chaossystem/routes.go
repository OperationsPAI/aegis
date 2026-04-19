package chaossystem

import (
	"aegis/consts"
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesAdmin(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceAdmin,
		Name:     "chaossystem.admin",
		Register: func(v2 *gin.RouterGroup) {
			systems := v2.Group("/systems", middleware.JWTAuth())
			{
				systemRead := systems.Group("", middleware.RequireSystemRead)
				{
					systemRead.GET("", handler.ListSystems)
					systemRead.GET("/:id", handler.GetSystem)
					systemRead.GET("/:id/metadata", handler.ListMetadata)
				}

				systemConfigure := systems.Group("", middleware.RequireSystemConfigure)
				{
					systemConfigure.POST("", handler.CreateSystem)
					systemConfigure.PUT("/:id", handler.UpdateSystem)
					systemConfigure.POST("/:id/metadata", handler.UpsertMetadata)
				}

				systems.DELETE("/:id", middleware.RequirePermission(consts.PermSystemManage), handler.DeleteSystem)
			}
		},
	}
}
