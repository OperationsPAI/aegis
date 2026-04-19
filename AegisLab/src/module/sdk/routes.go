package sdk

import (
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesSDK(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "sdk.sdk",
		Register: func(v2 *gin.RouterGroup) {
			sdkEval := v2.Group("/sdk/evaluations", middleware.JWTAuth(), middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:evaluations:*", "sdk:evaluations:read"))
			{
				sdkEval.GET("", handler.ListEvaluations)
				sdkEval.GET("/experiments", handler.ListExperiments)
				sdkEval.GET("/:id", handler.GetEvaluation)
			}

			sdkData := v2.Group("/sdk/datasets", middleware.JWTAuth(), middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:datasets:*", "sdk:datasets:read"))
			{
				sdkData.GET("", handler.ListDatasetSamples)
			}
		},
	}
}
