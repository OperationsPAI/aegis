package evaluation

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortal contributes the portal-only evaluation endpoint.
// The route shape is preserved exactly from the central router wiring:
// DELETE /api/v2/evaluations/:id
func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "evaluation.portal",
		Register: func(v2 *gin.RouterGroup) {
			evaluations := v2.Group("/evaluations", middleware.TrustedHeaderAuth())
			{
				evaluations.DELETE("/:id", handler.DeleteEvaluation)
			}
		},
	}
}

// RoutesSDK contributes the SDK-consumable evaluation endpoints.
// These handlers were already exposed on /api/v2/evaluations/* (not
// /api/v2/sdk/*), so the self-registered route tree keeps that contract.
func RoutesSDK(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "evaluation.sdk",
		Register: func(v2 *gin.RouterGroup) {
			evaluations := v2.Group("/evaluations", middleware.TrustedHeaderAuth())
			{
				evaluations.POST("/datapacks", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKEvaluationsAll, consts.ScopeSDKEvaluationsRead), handler.ListDatapackEvaluationResults)
				evaluations.POST("/datasets", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKEvaluationsAll, consts.ScopeSDKEvaluationsRead), handler.ListDatasetEvaluationResults)
				evaluations.GET("", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKEvaluationsAll, consts.ScopeSDKEvaluationsRead), handler.ListEvaluations)
				evaluations.GET("/:id", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKEvaluationsAll, consts.ScopeSDKEvaluationsRead), handler.GetEvaluation)
			}
		},
	}
}
