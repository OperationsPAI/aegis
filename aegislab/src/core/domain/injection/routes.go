package injection

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes registers every injection endpoint once. Project-scoped paths
// gate via RequireProject* perms; global /injections/* paths gate via
// RequireInjection{Read,Update,Delete,Clone,Download,Upload}.
func Routes(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "injection",
		Register: func(v2 *gin.RouterGroup) {
			projects := v2.Group("/projects")
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
						injectionRead.POST("/search", handler.SearchProjectInjections)
					}

					injectionExecute := injections.Group("", middleware.RequireProjectInjectionExecute)
					{
						injectionExecute.POST("/inject", handler.SubmitProjectFaultInjection)
						injectionExecute.POST("/build", handler.SubmitProjectDatapackBuilding)
					}
				}
			}

			injections := v2.Group("/injections")
			{
				read := injections.Group("", middleware.RequireInjectionRead)
				{
					read.GET("/systems", handler.GetSystemMapping)
					read.GET("/:id", handler.GetInjection)
					read.GET("/:id/datapack-schema", handler.GetDatapackSchema)
					read.GET("/:id/logs", handler.GetInjectionLogs)
					read.GET("/:id/logs/histogram", handler.GetInjectionLogsHistogram)
					read.GET("/:id/timeline", handler.GetInjectionTimeline)
					read.GET("/:id/diagnose", handler.DiagnoseDatapack)
				}

				download := injections.Group("", middleware.RequireInjectionDownload)
				{
					download.GET("/:id/download", handler.DownloadDatapack)
					download.GET("/:id/files", handler.ListDatapackFiles)
					download.GET("/:id/files/download", handler.DownloadDatapackFile)
					download.GET("/:id/files/query", handler.QueryDatapackFile)
					download.POST("/:id/datapack-query", handler.QueryDatapack)
				}

				injections.POST("/:id/clone", middleware.RequireInjectionClone, handler.CloneInjection)
				injections.PATCH("/:id/labels", middleware.RequireInjectionUpdate, handler.ManageInjectionCustomLabels)
				injections.PATCH("/labels/batch", middleware.RequireInjectionUpdate, handler.BatchManageInjectionLabels)
				injections.PUT("/:id/groundtruth", middleware.RequireInjectionUpdate, handler.UpdateGroundtruth)
				injections.POST("/batch-delete", middleware.RequireInjectionDelete, handler.BatchDeleteInjections)
				injections.POST("/upload", middleware.RequireInjectionUpload, handler.UploadDatapack)
				injections.POST("/:id/cancel", middleware.RequireTaskStop, handler.CancelInjection)
			}
		},
	}
}
