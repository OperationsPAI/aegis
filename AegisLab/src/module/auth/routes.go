package auth

import (
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesPublic(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePublic,
		Name:     "auth.public",
		Register: func(v2 *gin.RouterGroup) {
			auth := v2.Group("/auth")
			{
				auth.POST("/login", handler.Login)
				auth.POST("/register", handler.Register)
				auth.POST("/refresh", handler.RefreshToken)

				authProtected := auth.Group("", middleware.JWTAuth(), middleware.RequireHumanUserAuth())
				{
					authProtected.POST("/logout", handler.Logout)
					authProtected.POST("/change-password", handler.ChangePassword)
					authProtected.GET("/profile", handler.GetProfile)
				}
			}
		},
	}
}

func RoutesSDK(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "auth.sdk",
		Register: func(v2 *gin.RouterGroup) {
			auth := v2.Group("/auth")
			{
				auth.POST("/api-key/token", handler.ExchangeAPIKeyToken)
			}
		},
	}
}

func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "auth.portal",
		Register: func(v2 *gin.RouterGroup) {
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
