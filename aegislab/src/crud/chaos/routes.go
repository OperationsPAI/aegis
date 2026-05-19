package chaos

import (
	"aegis/platform/framework"

	"github.com/gin-gonic/gin"
)

// Routes registers the §5 route table. Step 1 wires up:
//
//   - PUT  /v1beta/systems/{name}
//   - GET  /v1beta/systems/{name}
//   - POST /v1beta/systems/{sys}/points:import
//   - POST /v1beta/injections
//   - GET  /v1beta/injections/{id}
//   - DEL  /v1beta/injections/{id}
//   - GET  /v1beta/capabilities
//   - GET  /v1beta/capabilities/{name}
//   - GET  /v1beta/manifest-schema.json
//
// Other §5 endpoints exist but return 501 — they land in steps 2-5.
func Routes(h *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Name:             "chaos.v1beta",
		BasePath:         "/v1beta",
		SkipDefaultChain: true,
		Register: func(g *gin.RouterGroup) {
			g.GET("/manifest-schema.json", h.ManifestSchema)

			g.GET("/systems", notImplemented)
			g.PUT("/systems/:name", h.PutSystem)
			g.GET("/systems/:name", h.GetSystem)
			g.DELETE("/systems/:name", notImplemented)

			g.GET("/systems/:name/services", notImplemented)
			g.GET("/systems/:name/services/:svc", notImplemented)
			g.GET("/systems/:name/services/:svc/versions", notImplemented)

			g.GET("/systems/:name/points", notImplemented)
			g.GET("/systems/:name/services/:svc/points", notImplemented)
			g.POST("/systems/:name/services/:svc/points", notImplemented)
			g.GET("/points/:id", notImplemented)
			g.DELETE("/points/:id", notImplemented)
			// The :import alias under /systems/:sys/points:import. Gin's
			// path syntax requires a literal segment; the design's "POST
			// /systems/{sys}/points:import" is mounted as the explicit
			// path below.
			g.POST("/systems/:sys/points:import", h.ImportPoints)

			g.GET("/capabilities", h.ListCapabilities)
			g.GET("/capabilities/:name", h.GetCapability)
			g.GET("/capabilities/:name/matrix", notImplemented)

			g.GET("/executors", notImplemented)
			g.POST("/executors/:name:probe", notImplemented)

			g.POST("/injections", h.CreateInjection)
			g.GET("/injections/:id", h.GetInjection)
			g.DELETE("/injections/:id", h.DeleteInjection)
			g.POST("/injections:preview", notImplemented)

			g.POST("/injection-batches", notImplemented)
			g.GET("/injection-batches/:id", notImplemented)
			g.DELETE("/injection-batches/:id", notImplemented)

			g.POST("/guided-sessions", notImplemented)
			g.POST("/guided-sessions/:tok/step", notImplemented)
			g.POST("/guided-sessions/:tok:commit", notImplemented)
		},
	}
}
