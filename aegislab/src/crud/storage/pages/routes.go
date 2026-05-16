package pages

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortal mounts the management API. Each handler's swagger block
// carries an x-api-type annotation listing both portal and sdk audiences,
// so a single registration covers both — the SDK generator picks them up
// from swagger. A second registrar would just duplicate the handler chain
// and panic gin at startup.
func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "pages.portal",
		Register: func(v2 *gin.RouterGroup) {
			g := v2.Group("/pages")
			{
				// Public listing + detail are intentionally unauthenticated
				// for anonymous callers; the service layer collapses private
				// sites to 404 for non-owners to avoid an existence oracle.
				g.GET("/public", middleware.OptionalJWTAuth(), handler.ListPublic)
				g.GET("/:id", middleware.OptionalJWTAuth(), handler.Detail)

				authed := g.Group("", middleware.JWTAuth(), middleware.RequireHumanUserAuth())
				{
					authed.POST("", middleware.RequireAnyPermission(pagesWritePerms), handler.CreatePage)
					authed.GET("", middleware.RequireAnyPermission(pagesReadPerms), handler.ListMine)
					authed.PATCH("/:id", middleware.RequireAnyPermission(pagesWritePerms), handler.UpdatePage)
					authed.DELETE("/:id", middleware.RequireAnyPermission(pagesWritePerms), handler.DeletePage)
					authed.POST("/:id/upload", middleware.RequireAnyPermission(pagesWritePerms), handler.ReplacePage)
				}
			}
		},
	}
}

// RoutesEngine mounts the public SSR + static asset routes at the
// engine root (BasePath="/"). Use BasePath sparingly — most API surface
// should live under /api/v2 via Audience-mounted registrars.
func RoutesEngine(render *RenderHandler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Name:     "pages.ssr",
		BasePath: "/",
		Register: func(root *gin.RouterGroup) {
			root.GET("/p/:slug", middleware.OptionalJWTAuth(), render.Render)
			root.GET("/p/:slug/*filepath", middleware.OptionalJWTAuth(), render.Render)
			root.GET("/static/pages/*filepath", ServeStaticAssets)
		},
	}
}
