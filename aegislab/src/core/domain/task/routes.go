package task

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortal contributes the task module's portal routes to the
// framework route group.
func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "task",
		Register: func(v2 *gin.RouterGroup) {
			tasks := v2.Group("/tasks", middleware.TrustedHeaderAuth())
			{
				taskRead := tasks.Group("", middleware.RequireTaskRead)
				{
					taskRead.GET("", handler.ListTasks)
					taskRead.GET("/:task_id", handler.GetTask)
					taskRead.GET("/:task_id/logs/ws", handler.GetTaskLogsWS)
				}

				tasks.POST("/batch-delete", middleware.RequireTaskDelete, handler.BatchDelete)
				tasks.POST("/:task_id/expedite", middleware.RequireTaskExecute, handler.ExpediteTask)
				tasks.POST("/:task_id/cancel", middleware.RequireTaskStop, handler.CancelTask)
			}
		},
	}
}
