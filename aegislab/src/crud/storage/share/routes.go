package share

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "share.portal",
		Register: func(v2 *gin.RouterGroup) {
			g := v2.Group("/share", middleware.TrustedHeaderAuth())
			{
				g.POST("/upload", handler.Upload)
				g.GET("", handler.List)
				g.GET("/", handler.List)
				g.GET("/:code", handler.GetOne)
				g.DELETE("/:code", handler.Revoke)
			}
		},
	}
}

// MountPublic attaches `/s/:code` to the bare engine (no /api/v2 prefix,
// no auth middleware). Invoked from boot/blob/options.go via fx.Invoke
// after the engine is built.
func MountPublic(engine *gin.Engine, handler *Handler) {
	engine.GET("/s/:code", handler.Redirect)
}
