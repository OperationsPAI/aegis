package sdk

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesSDK contributes the SDK module's HTTP routes to the framework's
// `group:"routes"` value-group.
//
// The SDK module only exposes SDK-audience routes under /api/v2/sdk/*, so a
// single RouteRegistrar is sufficient here.
func RoutesSDK(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "sdk",
		Register: func(v2 *gin.RouterGroup) {
			sdkEval := v2.Group("/sdk/evaluations", middleware.TrustedHeaderAuth(), middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKEvaluationsAll, consts.ScopeSDKEvaluationsRead))
			{
				sdkEval.GET("", handler.ListEvaluations)
				sdkEval.GET("/experiments", handler.ListExperiments)
				sdkEval.GET("/:id", handler.GetEvaluation)
			}

			sdkData := v2.Group("/sdk/datasets", middleware.TrustedHeaderAuth(), middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKDatasetsAll, consts.ScopeSDKDatasetsRead))
			{
				sdkData.GET("", handler.ListDatasetSamples)
			}
		},
	}
}
