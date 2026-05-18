package injection

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes registers every injection endpoint once. Project-scoped paths
// gate via RequireProject* perms; the global /injections/* paths are
// open to any authenticated caller, except cancel which needs
// RequireTaskStop and observation reads which need RequireProjectRead.
func Routes(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "injection",
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
						injectionRead.POST("/search", handler.SearchProjectInjections)
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
