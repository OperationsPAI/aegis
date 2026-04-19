package evaluation

import (
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "evaluation.portal",
		Register: func(v2 *gin.RouterGroup) {
			evaluations := v2.Group("/evaluations", middleware.JWTAuth())
			{
				evaluations.DELETE("/:id", handler.DeleteEvaluation)
			}
		},
	}
}

func RoutesSDK(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "evaluation.sdk",
		Register: func(v2 *gin.RouterGroup) {
			evaluations := v2.Group("/evaluations", middleware.JWTAuth())
			{
				evaluations.POST("/datapacks", middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:evaluations:*", "sdk:evaluations:read"), handler.ListDatapackEvaluationResults)
				evaluations.POST("/datasets", middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:evaluations:*", "sdk:evaluations:read"), handler.ListDatasetEvaluationResults)
				evaluations.GET("", middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:evaluations:*", "sdk:evaluations:read"), handler.ListEvaluations)
				evaluations.GET("/:id", middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:evaluations:*", "sdk:evaluations:read"), handler.GetEvaluation)
			}
		},
	}
}
