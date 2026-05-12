package configcenter

import (
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesAdmin mounts the admin surface. Writes require admin; reads
// require any authenticated principal. The SSE watch stream is also
// auth-gated — only the remote configcenterclient subscribes here.
func RoutesAdmin(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceAdmin,
		Name:     "configcenter.admin",
		Register: func(v2 *gin.RouterGroup) {
			cfg := v2.Group("/config", middleware.JWTAuth())
			{
				cfg.GET("/:namespace", handler.List)
				cfg.GET("/:namespace/watch", handler.Watch)
				cfg.GET("/:namespace/:key", handler.Get)
				cfg.GET("/:namespace/:key/history", handler.History)
				cfg.PUT("/:namespace/:key", middleware.RequireSystemAdmin(), handler.Set)
				cfg.DELETE("/:namespace/:key", middleware.RequireSystemAdmin(), handler.Delete)
			}
		},
	}
}
