package chaoshooks

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes mounts the webhook receivers under /api/v1/hooks/*. Auth uses
// TrustedHeaderAuth because aegis-chaos is a trusted in-cluster caller,
// never a user-facing surface (design §10.1, scope `webhook:chaos-receiver`).
// BasePath is used because /api/v1 sits outside the /api/v2 portal/SDK
// surface — these are inter-service RPC, not API surface.
func Routes(h *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Name:     "hooks.chaos",
		BasePath: "/api/v1/hooks",
		Register: func(g *gin.RouterGroup) {
			g.Use(middleware.TrustedHeaderAuth())
			g.POST("/chaos", middleware.RequireScope("chaos.webhook.write"), h.Singleton)
			g.POST("/chaos-batch", middleware.RequireScope("chaos.webhook.write"), h.Batch)
		},
	}
}
