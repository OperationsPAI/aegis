package sso

import (
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterClientRoutes mounts the OIDC client management API on the SSO
// process's gin engine. The SSO HTTP surface is rooted (no /api/v2 prefix)
// so admin endpoints sit alongside the spec-mandated `/v1/*` paths in §5
// of sso-extraction-design.md.
func RegisterClientRoutes(engine *gin.Engine, handler *Handler) {
	g := engine.Group("/v1/clients", middleware.JWTAuth())
	{
		g.POST("", handler.Create)
		g.GET("", handler.List)
		g.GET("/:id", handler.Get)
		g.PUT("/:id", handler.Update)
		// gin path parsing rejects ":rotate" suffix on a wildcard segment;
		// use the same colon-action shape as the design doc by lifting the
		// id into the URL path before the action.
		g.POST("/:id/rotate", handler.Rotate)
		g.DELETE("/:id", handler.Delete)
	}
}
