package pages

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortal mounts the human-portal-facing management API.
func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "pages.portal",
		Register: func(v2 *gin.RouterGroup) {
			g := v2.Group("/pages")
			{
				g.GET("/public", middleware.OptionalJWTAuth(), handler.ListPublic)

				authed := g.Group("", middleware.JWTAuth(), middleware.RequireHumanUserAuth())
				{
					authed.POST("", handler.CreatePage)
					authed.GET("", handler.ListMine)
					authed.PATCH("/:id", handler.UpdatePage)
					authed.DELETE("/:id", handler.DeletePage)
					authed.POST("/:id/upload", handler.ReplacePage)
				}
				// Detail uses OptionalJWTAuth so anonymous callers can read
				// public listings but private sites require the owner.
				g.GET("/:id", middleware.OptionalJWTAuth(), handler.Detail)
			}
		},
	}
}

// RoutesSDK mirrors the portal surface for SDK callers. The handler logic
// is the same — separating audiences keeps the OpenAPI tagging clean.
func RoutesSDK(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "pages.sdk",
		Register: func(v2 *gin.RouterGroup) {
			g := v2.Group("/pages")
			{
				g.GET("/public", middleware.OptionalJWTAuth(), handler.ListPublic)
				authed := g.Group("", middleware.JWTAuth(), middleware.RequireHumanUserAuth())
				{
					authed.POST("", handler.CreatePage)
					authed.GET("", handler.ListMine)
					authed.PATCH("/:id", handler.UpdatePage)
					authed.DELETE("/:id", handler.DeletePage)
					authed.POST("/:id/upload", handler.ReplacePage)
				}
				g.GET("/:id", middleware.OptionalJWTAuth(), handler.Detail)
			}
		},
	}
}

// RoutesEngine mounts the public SSR + static asset routes at the
// engine root (no /api/v2 prefix).
func RoutesEngine(render *RenderHandler) framework.EngineRegistrar {
	return framework.EngineRegistrar{
		Name: "pages.ssr",
		Register: func(engine *gin.Engine) {
			engine.GET("/p/:slug", middleware.OptionalJWTAuth(), render.Render)
			engine.GET("/p/:slug/*filepath", middleware.OptionalJWTAuth(), render.Render)
			engine.GET("/static/pages/*filepath", ServeStaticAssets)
		},
	}
}
