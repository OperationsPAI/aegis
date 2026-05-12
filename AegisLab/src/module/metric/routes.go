package metric

import (
	"aegis/consts"
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesSDK contributes the metric module's SDK-readable endpoints.
// These routes were previously registered centrally in router/sdk.go.
func RoutesSDK(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "metric.sdk",
		Register: func(v2 *gin.RouterGroup) {
			metrics := v2.Group("/metrics", middleware.JWTAuth())
			{
				metrics.GET("/algorithms", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKMetricsAll, consts.ScopeSDKMetricsRead), handler.GetAlgorithmMetrics)
				metrics.GET("/executions", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKMetricsAll, consts.ScopeSDKMetricsRead), handler.GetExecutionMetrics)
				metrics.GET("/injections", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKMetricsAll, consts.ScopeSDKMetricsRead), handler.GetInjectionMetrics)
			}
		},
	}
}
