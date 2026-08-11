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

// requireChaosPrincipal closes the /v1beta exposure surface: the chaos
// endpoints are service-to-service, but the upstream auth chain
// (RequireServiceAccount → NewChaosAuthFromEnv → TrustedHeaderAuth/JWTAuth)
// falls through to ordinary user-JWT auth, so any authenticated user could
// otherwise create/delete injections cluster-wide (there is no scope gate on
// the handlers). This runs last and admits only a service token (the
// chaos-client SA or the static inbound bearer — both set IsServiceToken) or
// an admin; ordinary/default users get 403, anonymous is already 401 upstream.
func requireChaosPrincipal() gin.HandlerFunc {
	return func(c *gin.Context) {
		if middleware.IsServiceToken(c) || middleware.IsCurrentUserAdmin(c) {
			c.Next()
			return
		}
		dto.ErrorResponse(c, http.StatusForbidden,
			"Forbidden: chaos endpoints are restricted to service accounts")
		c.Abort()
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

			// SA token first (backend mints a service-account token for
			// chaos-client at boot); on missing/non-SA bearer the chain
			// falls through to the static-bearer path.
			g.Use(middleware.RequireServiceAccount(db, verifier.Resolve, "chaos-client"))
			// requireChaosPrincipal runs LAST in the auth chain: the /v1beta
			// chaos surface is service-to-service, so reject ordinary/default
			// users that authenticate via the TrustedHeaderAuth/JWTAuth
			// fallthrough. Only a service token (chaos-client SA or the static
			// inbound bearer, both set IsServiceToken) or an admin is allowed.
			auth := g.Group("", NewChaosAuthFromEnv(), requireChaosPrincipal())

			auth.PUT("/systems/:sys", h.PutSystem)
			auth.GET("/systems/:sys", h.GetSystem)

			auth.GET("/systems/:sys/points", h.ListSystemPoints)
			auth.GET("/systems/:sys/points/export", h.ExportSystemPoints)
			auth.POST("/systems/:sys/points/import", h.ImportPoints)
			auth.POST("/systems/:sys/points/sweep", h.SweepPoints)
			auth.GET("/systems/:sys/candidates", h.ListSystemCandidates)

			auth.POST("/guided/resolve", h.GuidedResolve)
			auth.POST("/guided/apply-next", h.GuidedApplyNext)

			auth.GET("/capabilities", h.ListCapabilities)
			auth.GET("/capabilities/:name", h.GetCapability)

			auth.POST("/injections", h.CreateInjection)
			auth.GET("/injections/:id", h.GetInjection)
			auth.GET("/injections/:id/events", h.StreamInjectionEvents)
			auth.DELETE("/injections/by-task/:taskID", h.DeleteInjectionByTask)
			auth.DELETE("/injections/:id", h.DeleteInjection)

			auth.POST("/injection-batches", h.CreateInjectionBatch)
			auth.GET("/injection-batches/:id", h.GetInjectionBatch)
			auth.DELETE("/injection-batches/:id", h.DeleteInjectionBatch)
		},
	}
}
