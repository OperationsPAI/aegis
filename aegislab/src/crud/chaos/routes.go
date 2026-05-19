package chaos

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes mounts the §5 surface under /v1beta. Step 1 wires systems,
// points/import, singleton injections, capabilities, manifest-schema;
// other endpoints return 501 until later steps.
//
// Route-syntax note: the design doc spells action verbs with a colon
// suffix (`:import`, `:probe`, `:preview`, `:commit`). Gin treats the
// segment after `:` as a path parameter and panics on a second `:` in
// the same segment, so we serialise actions as a trailing `/<verb>`
// path component. See the §5 addendum in
// aegislab/docs/aegis-chaos-design.md.
func Routes(h *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Name:             "chaos.v1beta",
		BasePath:         "/v1beta",
		SkipDefaultChain: true,
		Register: func(g *gin.RouterGroup) {
			// Manifest schema is the one read path chart authors hit from
			// CI / pre-commit hooks that don't carry tokens.
			g.GET("/manifest-schema.json", h.ManifestSchema)

			auth := g.Group("", middleware.TrustedHeaderAuth())

			auth.GET("/systems", notImplemented)
			auth.PUT("/systems/:sys", h.PutSystem)
			auth.GET("/systems/:sys", h.GetSystem)
			auth.DELETE("/systems/:sys", notImplemented)

			auth.GET("/systems/:sys/services", notImplemented)
			auth.GET("/systems/:sys/services/:svc", notImplemented)
			auth.GET("/systems/:sys/services/:svc/versions", notImplemented)

			auth.GET("/systems/:sys/points", h.ListSystemPoints)
			auth.GET("/systems/:sys/services/:svc/points", notImplemented)
			auth.GET("/points/:id", notImplemented)
			auth.DELETE("/points/:id", notImplemented)
			auth.POST("/systems/:sys/points/import", h.ImportPoints)

			auth.GET("/capabilities", h.ListCapabilities)
			auth.GET("/capabilities/:name", h.GetCapability)
			auth.GET("/capabilities/:name/matrix", notImplemented)

			auth.GET("/executors", notImplemented)
			auth.POST("/executors/:name/probe", notImplemented)

			auth.POST("/injections", h.CreateInjection)
			auth.GET("/injections/:id", h.GetInjection)
			auth.DELETE("/injections/:id", h.DeleteInjection)
			auth.POST("/injections/preview", notImplemented)

			auth.POST("/injection-batches", h.CreateInjectionBatch)
			auth.GET("/injection-batches/:id", h.GetInjectionBatch)
			auth.DELETE("/injection-batches/:id", h.DeleteInjectionBatch)

			auth.POST("/guided-sessions", notImplemented)
			auth.POST("/guided-sessions/:tok/step", notImplemented)
			auth.POST("/guided-sessions/:tok/commit", notImplemented)
		},
	}
}
