package execution

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes registers every execution endpoint once. Project-scoped paths
// gate via RequireProject*; global /executions/* paths gate via
// RequireExecution{Read,Update,Delete}. API-key scope checks are
// stacked on top for SDK callers.
func Routes(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "execution",
		Register: func(v2 *gin.RouterGroup) {
			projects := v2.Group("/projects")
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

			executions := v2.Group("/executions")
			{
				executions.GET("", middleware.RequireExecutionRead, handler.ListExecutions)
				executions.GET("/labels", middleware.RequireExecutionRead, middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKExecutionsAll, consts.ScopeSDKExecutionsRead), handler.ListAvailableExecutionLabels)
				executions.GET("/:execution_id", middleware.RequireExecutionRead, middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKExecutionsAll, consts.ScopeSDKExecutionsRead), handler.GetExecution)
				executions.PATCH("/:execution_id/labels", middleware.RequireExecutionUpdate, middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKExecutionsAll, consts.ScopeSDKExecutionsWrite), handler.ManageExecutionCustomLabels)
				executions.POST("/batch-delete", middleware.RequireExecutionDelete, handler.BatchDeleteExecutions)
				executions.POST("/compare", middleware.RequireExecutionRead, handler.CompareExecutions)
			}

			runtime := v2.Group("/executions", middleware.RequireServiceTokenAuth())
			{
				runtime.POST("/:execution_id/detector_results", handler.UploadDetectorResults)
				runtime.POST("/:execution_id/granularity_results", handler.UploadGranularityResults)
			}
		},
	}
}
