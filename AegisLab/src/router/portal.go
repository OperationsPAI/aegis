package router

import (
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func SetupPortalV2Routes(v2 *gin.RouterGroup, handlers *Handlers) {
	containers := v2.Group("/containers", middleware.JWTAuth())
	{
		containerRead := containers.Group("", middleware.RequireContainerRead)
		{
			containerRead.GET("", handlers.Container.ListContainers)
			containerRead.GET("/:container_id", handlers.Container.GetContainer)
		}

		containers.POST("", middleware.RequireContainerCreate, handlers.Container.CreateContainer)
		containers.PATCH("/:container_id", middleware.RequireContainerUpdate, handlers.Container.UpdateContainer)
		containers.PATCH("/:container_id/labels", middleware.RequireContainerUpdate, handlers.Container.ManageContainerCustomLabels)
		containers.DELETE("/:container_id", middleware.RequireContainerDelete, handlers.Container.DeleteContainer)
		containers.POST("/build", middleware.RequireContainerExecute, handlers.Container.SubmitContainerBuilding)

		containerVersions := containers.Group("/:container_id/versions")
		{
			containerVersionRead := containerVersions.Group("", middleware.RequireContainerVersionRead)
			{
				containerVersionRead.GET("", handlers.Container.ListContainerVersions)
				containerVersionRead.GET("/:version_id", handlers.Container.GetContainerVersion)
			}

			containerVersions.POST("", middleware.RequireContainerVersionCreate, handlers.Container.CreateContainerVersion)
			containerVersions.PATCH("/:version_id", middleware.RequireContainerVersionUpdate, handlers.Container.UpdateContainerVersion)
			containerVersions.DELETE("/:version_id", middleware.RequireContainerVersionDelete, handlers.Container.DeleteContainerVersion)
			containerVersions.POST("/:version_id/helm-chart", middleware.RequireContainerVersionUpload, handlers.Container.UploadHelmChart)
			containerVersions.POST("/:version_id/helm-values", middleware.RequireContainerVersionUpload, handlers.Container.UploadHelmValueFile)
		}
	}

	flatContainerVersions := v2.Group("/container-versions", middleware.JWTAuth())
	{
		flatContainerVersions.PATCH("/:id/image", middleware.RequireContainerVersionUpdate, handlers.Container.SetContainerVersionImage)
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
