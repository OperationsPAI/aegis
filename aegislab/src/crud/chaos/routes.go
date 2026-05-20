package chaos

import (
	"net/http"

	"aegis/platform/dto"
	"aegis/platform/framework"

	"github.com/gin-gonic/gin"
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
func Routes(h *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Name:             "chaos.v1beta",
		BasePath:         "/v1beta",
		SkipDefaultChain: true,
		Register: func(g *gin.RouterGroup) {
			g.Use(limitRequestBody())

			// Manifest schema is the one read path chart authors hit from
			// CI / pre-commit hooks that don't carry tokens.
			g.GET("/manifest-schema.json", h.ManifestSchema)

			auth := g.Group("", NewChaosAuthFromEnv())

			auth.PUT("/systems/:sys", h.PutSystem)
			auth.GET("/systems/:sys", h.GetSystem)

			auth.GET("/systems/:sys/points", h.ListSystemPoints)
			auth.POST("/systems/:sys/points/import", h.ImportPoints)

			auth.GET("/capabilities", h.ListCapabilities)
			auth.GET("/capabilities/:name", h.GetCapability)

			auth.POST("/injections", h.CreateInjection)
			auth.GET("/injections/:id", h.GetInjection)
			auth.DELETE("/injections/:id", h.DeleteInjection)

			auth.POST("/injection-batches", h.CreateInjectionBatch)
			auth.GET("/injection-batches/:id", h.GetInjectionBatch)
			auth.DELETE("/injection-batches/:id", h.DeleteInjectionBatch)
		},
	}
}
