package router

import (
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func SetupAdminV2Routes(v2 *gin.RouterGroup, handlers *Handlers) {
	system := v2.Group("/system", middleware.JWTAuth(), middleware.RequireSystemRead)
	{
		system.GET("/metrics", handlers.SystemMetric.GetSystemMetrics)
		system.GET("/metrics/history", handlers.SystemMetric.GetSystemMetricsHistory)
		audit := system.Group("/audit", middleware.RequireAuditRead)
		{
			audit.GET("", handlers.System.ListAuditLogs)
			audit.GET("/:id", handlers.System.GetAuditLog)
		}

		configs := system.Group("/configs")
		{
			configsRead := configs.Group("", middleware.RequireConfigurationRead)
			{
				configsRead.GET("", handlers.System.ListConfigs)
				configsRead.GET("/:config_id", handlers.System.GetConfig)
				configsRead.GET("/:config_id/histories", handlers.System.ListConfigHistories)
			}

			configs.PATCH("/:config_id", middleware.RequireConfigurationUpdate, handlers.System.UpdateConfigValue)
			configs.POST("/:config_id/value/rollback", middleware.RequireConfigurationUpdate, handlers.System.RollbackConfigValue)
			configs.PUT("/:config_id/metadata", middleware.RequireConfigurationConfigure, handlers.System.UpdateConfigMetadata)
			configs.POST("/:config_id/metadata/rollback", middleware.RequireConfigurationConfigure, handlers.System.RollbackConfigMetadata)
		}

		system.GET("/health", handlers.System.GetHealth)

		monitor := system.Group("/monitor")
		monitor.POST("/metrics", handlers.System.GetMetrics)
		monitor.GET("/info", handlers.System.GetSystemInfo)
		monitor.GET("/namespaces/locks", handlers.System.ListNamespaceLocks)
		monitor.GET("/tasks/queue", handlers.System.ListQueuedTasks)
	}
}
