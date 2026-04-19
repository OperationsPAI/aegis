package execution

import (
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
				executions.GET("/labels", middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:executions:*", "sdk:executions:read"), handler.ListAvailableExecutionLabels)
				executions.POST("/batch-delete", handler.BatchDeleteExecutions)
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
				executions.GET("/:id", middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:executions:*", "sdk:executions:read"), handler.GetExecution)
				executions.PATCH("/:id/labels", middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:executions:*", "sdk:executions:write"), handler.ManageExecutionCustomLabels)
			}

			runtime := v2.Group("/executions", middleware.RequireServiceTokenAuth())
			{
				runtime.POST("/:execution_id/detector_results", handler.UploadDetectorResults)
				runtime.POST("/:execution_id/granularity_results", handler.UploadGranularityResults)
			}
		},
	}
}
