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

	sdkEval := v2.Group("/sdk/evaluations", middleware.JWTAuth(), middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:evaluations:*", "sdk:evaluations:read"))
	{
		sdkEval.GET("", handlers.SDK.ListEvaluations)
		sdkEval.GET("/experiments", handlers.SDK.ListExperiments)
		sdkEval.GET("/:id", handlers.SDK.GetEvaluation)
	}

	sdkData := v2.Group("/sdk/datasets", middleware.JWTAuth(), middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:datasets:*", "sdk:datasets:read"))
	{
		sdkData.GET("", handlers.SDK.ListDatasetSamples)
	}

	projects := v2.Group("/projects", middleware.JWTAuth())
	{
		executions := projects.Group("/:project_id/executions")
		{
			executionRead := executions.Group("", middleware.RequireProjectRead)
			{
				executionRead.GET("", handlers.Execution.ListProjectExecutions)
			}

			executionExecute := executions.Group("", middleware.RequireProjectExecutionExecute)
			{
				executionExecute.POST("/execute", handlers.Execution.SubmitAlgorithmExecution)
			}
		}
	}
}
