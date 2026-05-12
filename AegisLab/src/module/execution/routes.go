package execution

import (
	"aegis/consts"
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "execution.portal",
		Register: func(v2 *gin.RouterGroup) {
			executions := v2.Group("/executions", middleware.JWTAuth())
			{
				executions.GET("/labels", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKExecutionsAll, consts.ScopeSDKExecutionsRead), handler.ListAvailableExecutionLabels)
				executions.POST("/batch-delete", handler.BatchDeleteExecutions)
				executions.POST("/compare", handler.CompareExecutions)
			}
		},
	}
}

func RoutesSDK(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "execution.sdk",
		Register: func(v2 *gin.RouterGroup) {
			projects := v2.Group("/projects", middleware.JWTAuth())
			{
				executions := projects.Group("/:project_id/executions")
				{
					executionRead := executions.Group("", middleware.RequireProjectRead)
					{
						executionRead.GET("", handler.ListProjectExecutions)
					}

					executionExecute := executions.Group("", middleware.RequireProjectExecutionExecute)
					{
						executionExecute.POST("/execute", handler.SubmitAlgorithmExecution)
					}
				}
			}

			executions := v2.Group("/executions", middleware.JWTAuth())
			{
				executions.GET("/:execution_id", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKExecutionsAll, consts.ScopeSDKExecutionsRead), handler.GetExecution)
				executions.PATCH("/:execution_id/labels", middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKExecutionsAll, consts.ScopeSDKExecutionsWrite), handler.ManageExecutionCustomLabels)
			}

			runtime := v2.Group("/executions", middleware.JWTAuth(), middleware.RequireServiceTokenAuth())
			{
				runtime.POST("/:execution_id/detector_results", handler.UploadDetectorResults)
				runtime.POST("/:execution_id/granularity_results", handler.UploadGranularityResults)
			}
		},
	}
}
