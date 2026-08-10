package evaluation

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes registers every evaluation endpoint once. The list/get/search
// paths gate API-key callers via RequireAPIKeyScopesAny; session callers
// pass through unchanged. DELETE stays portal-only (no scope gate).
func Routes(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "evaluation",
		Register: func(v2 *gin.RouterGroup) {
			evaluations := v2.Group("/evaluations")
			{
				evaluations.DELETE("/:id", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKEvaluationsAll), handler.DeleteEvaluation)
				evaluations.POST("/datapacks", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKEvaluationsAll, consts.ScopeSDKEvaluationsRead), handler.ListDatapackEvaluationResults)
				evaluations.POST("/datasets", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKEvaluationsAll, consts.ScopeSDKEvaluationsRead), handler.ListDatasetEvaluationResults)
				evaluations.GET("", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKEvaluationsAll, consts.ScopeSDKEvaluationsRead), handler.ListEvaluations)
				evaluations.GET("/:id", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKEvaluationsAll, consts.ScopeSDKEvaluationsRead), handler.GetEvaluation)
			}
		},
	}
}
