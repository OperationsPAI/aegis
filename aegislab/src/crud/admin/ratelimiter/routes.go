package ratelimiter

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes is the ratelimiter module's portal route registrar.
// These endpoints were previously registered centrally in router/portal.go.
func Routes(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "ratelimiter",
		Register: func(v2 *gin.RouterGroup) {
			rateLimiters := v2.Group("/rate-limiters", middleware.TrustedHeaderAuth())
			{
				rateLimiters.GET("", handler.ListRateLimiters)
				rateLimiterAdmin := rateLimiters.Group("", middleware.RequireSystemAdmin())
				{
					rateLimiterAdmin.DELETE("/:bucket", handler.ResetRateLimiter)
					rateLimiterAdmin.POST("/gc", handler.GCRateLimiters)
				}
			}
		},
	}
}
