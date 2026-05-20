package chaos

import (
	"net/http"

	"aegis/platform/dto"
	"aegis/platform/framework"
	"aegis/platform/jwtkeys"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// chaosMaxRequestBytes caps inbound /v1beta request bodies. caller_metadata
// and batch_caller_metadata are unstructured maps with no other size guard.
const chaosMaxRequestBytes = 1 << 20

func limitRequestBody() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > chaosMaxRequestBytes {
			dto.ErrorResponse(c, http.StatusRequestEntityTooLarge,
				"request body exceeds 1 MiB limit")
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, chaosMaxRequestBytes)
		c.Next()
	}
}

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
func Routes(h *Handler, db *gorm.DB, verifier *jwtkeys.Verifier) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Name:             "chaos.v1beta",
		BasePath:         "/v1beta",
		SkipDefaultChain: true,
		Register: func(g *gin.RouterGroup) {
			g.Use(limitRequestBody())

			// Manifest schema is the one read path chart authors hit from
			// CI / pre-commit hooks that don't carry tokens.
			g.GET("/manifest-schema.json", h.ManifestSchema)

			// SA token first (backend mints rcabench-sa for chaos-client at
			// boot, see core/orchestrator/chaos_sa_token.go); on missing/
			// non-SA bearer the chain falls through to the static-bearer
			// path so kubectl smoke-tests with CHAOS_INBOUND_BEARER keep
			// working through the cutover.
			g.Use(middleware.RequireServiceAccount(db, verifier.Resolve, "chaos-client"))
			auth := g.Group("", NewChaosAuthFromEnv())

			auth.PUT("/systems/:sys", h.PutSystem)
			auth.GET("/systems/:sys", h.GetSystem)

			auth.GET("/systems/:sys/points", h.ListSystemPoints)
			auth.POST("/systems/:sys/points/import", h.ImportPoints)

			auth.GET("/capabilities", h.ListCapabilities)
			auth.GET("/capabilities/:name", h.GetCapability)

			auth.POST("/injections", h.CreateInjection)
			auth.GET("/injections/:id", h.GetInjection)
			auth.GET("/injections/:id/events", h.StreamInjectionEvents)
			auth.DELETE("/injections/:id", h.DeleteInjection)

			auth.POST("/injection-batches", h.CreateInjectionBatch)
			auth.GET("/injection-batches/:id", h.GetInjectionBatch)
			auth.DELETE("/injection-batches/:id", h.DeleteInjectionBatch)
		},
	}
}
