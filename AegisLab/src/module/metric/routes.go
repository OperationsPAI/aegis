package metric

import (
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
				metrics.GET("/algorithms", middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:metrics:*", "sdk:metrics:read"), handler.GetAlgorithmMetrics)
				metrics.GET("/executions", middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:metrics:*", "sdk:metrics:read"), handler.GetExecutionMetrics)
				metrics.GET("/injections", middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:metrics:*", "sdk:metrics:read"), handler.GetInjectionMetrics)
			}
		},
	}
}
