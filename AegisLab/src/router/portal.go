package router

import (
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func SetupPortalV2Routes(v2 *gin.RouterGroup, handlers *Handlers) {
	datasets := v2.Group("/datasets", middleware.JWTAuth())
	{
		datasetRead := datasets.Group("", middleware.RequireDatasetRead)
		{
			datasetRead.GET("", handlers.Dataset.ListDatasets)
			datasetRead.GET("/:dataset_id", handlers.Dataset.GetDataset)
			datasetRead.POST("/search", handlers.Dataset.SearchDataset)
		}

		datasets.POST("", middleware.RequireDatasetCreate, handlers.Dataset.CreateDataset)
		datasets.PATCH("/:dataset_id", middleware.RequireDatasetUpdate, handlers.Dataset.UpdateDataset)
		datasets.PATCH("/:dataset_id/labels", middleware.RequireDatasetUpdate, handlers.Dataset.ManageDatasetCustomLabels)
		datasets.DELETE("/:dataset_id", middleware.RequireDatasetDelete, handlers.Dataset.DeleteDataset)

		datasetVersions := datasets.Group("/:dataset_id/versions")
		{
			datasetVersionRead := datasetVersions.Group("", middleware.RequireDatasetVersionRead)
			{
				datasetVersionRead.GET("", handlers.Dataset.ListDatasetVersions)
				datasetVersionRead.GET("/:version_id", handlers.Dataset.GetDatasetVersion)
			}

			datasetVersions.POST("", middleware.RequireDatasetVersionCreate, handlers.Dataset.CreateDatasetVersion)
			datasetVersions.PATCH("/:version_id", middleware.RequireDatasetVersionUpdate, handlers.Dataset.UpdateDatasetVersion)
			datasetVersions.DELETE("/:version_id", middleware.RequireDatasetVersionDelete, handlers.Dataset.DeleteDatasetVersion)
		}
	}

	projects := v2.Group("/projects", middleware.JWTAuth())
	{
		projects.POST("/:project_id/injections/search", middleware.RequireProjectRead, handlers.Injection.SearchProjectInjections)
	}

	// /api/v2/labels routes moved to module/label/routes.go (Phase 3
	// reference migration). The label module self-registers via
	// framework.RouteRegistrar; see AegisLab/CONTRIBUTING.md.

	evaluations := v2.Group("/evaluations", middleware.JWTAuth())
	{
		evaluations.DELETE("/:id", handlers.Evaluation.DeleteEvaluation)
	}

	executions := v2.Group("/executions", middleware.JWTAuth())
	{
		executions.GET("/labels", middleware.RequireAPIKeyScopesAny("sdk:*", "sdk:executions:*", "sdk:executions:read"), handlers.Execution.ListAvailableExecutionLabels)
		executions.POST("/batch-delete", handlers.Execution.BatchDeleteExecutions)
	}

	injections := v2.Group("/injections", middleware.JWTAuth())
	{
		injections.PATCH("/labels/batch", handlers.Injection.BatchManageInjectionLabels)
		injections.POST("/batch-delete", handlers.Injection.BatchDeleteInjections)
		injections.POST("/upload", handlers.Injection.UploadDatapack)
		injections.PUT("/:id/groundtruth", handlers.Injection.UpdateGroundtruth)
	}

	tasks := v2.Group("/tasks", middleware.JWTAuth())
	{
		taskRead := tasks.Group("", middleware.RequireTaskRead)
		{
			taskRead.GET("", handlers.Task.ListTasks)
			taskRead.GET("/:task_id", handlers.Task.GetTask)
			taskRead.GET("/:task_id/logs/ws", handlers.Task.GetTaskLogsWS)
		}

		tasks.POST("/batch-delete", middleware.RequireTaskDelete, handlers.Task.BatchDelete)
		tasks.POST("/:task_id/expedite", middleware.RequireTaskExecute, handlers.Task.ExpediteTask)
	}

	rateLimiters := v2.Group("/rate-limiters", middleware.JWTAuth())
	{
		rateLimiters.GET("", handlers.RateLimiter.ListRateLimiters)
		rateLimiterAdmin := rateLimiters.Group("", middleware.RequireSystemAdmin())
		{
			rateLimiterAdmin.DELETE("/:bucket", handlers.RateLimiter.ResetRateLimiter)
			rateLimiterAdmin.POST("/gc", handlers.RateLimiter.GCRateLimiters)
		}
	}



	accessKeys := v2.Group("/api-keys", middleware.JWTAuth(), middleware.RequireHumanUserAuth())
	{
		accessKeys.GET("", handlers.Auth.ListAPIKeys)
		accessKeys.POST("", handlers.Auth.CreateAPIKey)
		accessKeys.GET("/:id", handlers.Auth.GetAPIKey)
		accessKeys.DELETE("/:id", handlers.Auth.DeleteAPIKey)
		accessKeys.POST("/:id/rotate", handlers.Auth.RotateAPIKey)
		accessKeys.POST("/:id/disable", handlers.Auth.DisableAPIKey)
		accessKeys.POST("/:id/enable", handlers.Auth.EnableAPIKey)
		accessKeys.POST("/:id/revoke", handlers.Auth.RevokeAPIKey)
	}
}
