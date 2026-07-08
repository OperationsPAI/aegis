package chaoshooks

import (
	"aegis/platform/framework"
	"aegis/platform/jwtkeys"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Routes mounts the webhook receivers under /api/v1/hooks/*. Auth tries an
// SA token first (chaos-service must present a non-revoked service-account
// token); on missing/non-SA bearer it falls through to TrustedHeaderAuth +
// JWTAuth.
// Design §10.1, scope `webhook:chaos-receiver`; BasePath is /api/v1
// because these are inter-service RPC, not /api/v2 portal/SDK surface.
func Routes(h *Handler, db *gorm.DB, verifier *jwtkeys.Verifier) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Name:     "hooks.chaos",
		BasePath: "/api/v1/hooks",
		Register: func(g *gin.RouterGroup) {
			g.Use(middleware.RequireServiceAccount(db, verifier.Resolve, "chaos-service"))
			g.Use(middleware.TrustedHeaderAuth())
			g.POST("/chaos", middleware.RequireScope("chaos.webhook.write"), h.Singleton)
			g.POST("/chaos-batch", middleware.RequireScope("chaos.webhook.write"), h.Batch)
		},
	}
}
