package injection

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesSDK(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "injection-sdk",
		Register: func(v2 *gin.RouterGroup) {
			projects := v2.Group("/projects", middleware.TrustedHeaderAuth())
			{
				injections := projects.Group("/:project_id/injections")
				{
					injectionRead := injections.Group("", middleware.RequireProjectRead)
					{
						analysis := injectionRead.Group("/analysis")
						{
							analysis.GET("/no-issues", handler.ListProjectFaultInjectionNoIssues)
							analysis.GET("/with-issues", handler.ListProjectFaultInjectionWithIssues)
						}

						injectionRead.GET("", handler.ListProjectInjections)
					}

					injectionExecute := injections.Group("", middleware.RequireProjectInjectionExecute)
					{
						injectionExecute.POST("/inject", handler.SubmitProjectFaultInjection)
						injectionExecute.POST("/build", handler.SubmitProjectDatapackBuilding)
					}
				}
			}

			injections := v2.Group("/injections", middleware.TrustedHeaderAuth())
			{
				injections.GET("/systems", handler.GetSystemMapping)
				injections.GET("/:id", handler.GetInjection)
				injections.POST("/:id/clone", handler.CloneInjection)
				injections.GET("/:id/download", handler.DownloadDatapack)
				injections.GET("/:id/files", handler.ListDatapackFiles)
				injections.GET("/:id/files/download", handler.DownloadDatapackFile)
				injections.GET("/:id/files/query", handler.QueryDatapackFile)
				injections.GET("/:id/datapack-schema", handler.GetDatapackSchema)
				injections.POST("/:id/datapack-query", handler.QueryDatapack)
				injections.PATCH("/:id/labels", handler.ManageInjectionCustomLabels)
			}
		},
	}
}

func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "injection-portal",
		Register: func(v2 *gin.RouterGroup) {
			projects := v2.Group("/projects", middleware.TrustedHeaderAuth())
			{
				projects.POST("/:project_id/injections/search", middleware.RequireProjectRead, handler.SearchProjectInjections)
			}

			injections := v2.Group("/injections", middleware.TrustedHeaderAuth())
			{
				injections.PATCH("/labels/batch", handler.BatchManageInjectionLabels)
				injections.POST("/batch-delete", handler.BatchDeleteInjections)
				injections.POST("/upload", handler.UploadDatapack)
				injections.POST("/:id/cancel", middleware.RequireTaskStop, handler.CancelInjection)
				injections.PUT("/:id/groundtruth", handler.UpdateGroundtruth)

				observation := injections.Group("", middleware.RequireProjectRead)
				{
					observation.GET("/:id/logs", handler.GetInjectionLogs)
					observation.GET("/:id/logs/histogram", handler.GetInjectionLogsHistogram)
					observation.GET("/:id/timeline", handler.GetInjectionTimeline)
				}
			}
		},
	}
}
