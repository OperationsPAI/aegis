package auth

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes registers every auth endpoint once. /auth/login,
// /auth/register, /auth/refresh, and /auth/api-key/token stay
// unauthenticated. /auth/logout, /auth/change-password, /auth/profile,
// and /api-keys/* require an authenticated human session.
func Routes(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePublic,
		Name:     "auth",
		Register: func(v2 *gin.RouterGroup) {
			auth := v2.Group("/auth")
			{
				auth.POST("/login", handler.Login)
				auth.POST("/register", handler.Register)
				auth.POST("/refresh", handler.RefreshToken)
				auth.POST("/api-key/token", handler.ExchangeAPIKeyToken)

				authProtected := auth.Group("", middleware.JWTAuth(), middleware.RequireHumanUserAuth())
				{
					authProtected.POST("/logout", handler.Logout)
					authProtected.POST("/change-password", handler.ChangePassword)
					authProtected.GET("/profile", handler.GetProfile)
				}
			}

			accessKeys := v2.Group("/api-keys", middleware.JWTAuth(), middleware.RequireHumanUserAuth())
			{
				accessKeys.GET("", handler.ListAPIKeys)
				accessKeys.POST("", handler.CreateAPIKey)
				accessKeys.GET("/:id", handler.GetAPIKey)
				accessKeys.DELETE("/:id", handler.DeleteAPIKey)
				accessKeys.POST("/:id/rotate", handler.RotateAPIKey)
				accessKeys.POST("/:id/disable", handler.DisableAPIKey)
				accessKeys.POST("/:id/enable", handler.EnableAPIKey)
				accessKeys.POST("/:id/revoke", handler.RevokeAPIKey)
			}
		},
	}
}
