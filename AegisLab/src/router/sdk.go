package router

import (
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func SetupSDKV2Routes(v2 *gin.RouterGroup, handlers *Handlers) {
	auth := v2.Group("/auth")
	{
		auth.POST("/api-key/token", handlers.Auth.ExchangeAPIKeyToken)
	}

	sdkData := v2.Group("/sdk/datasets", middleware.JWTAuth(), middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:datasets:*", "sdk:datasets:read"))
	{
		sdkData.GET("", handlers.SDK.ListDatasetSamples)
	}

	projects := v2.Group("/projects", middleware.JWTAuth())
	{}
}
