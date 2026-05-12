package system

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesAdmin contributes the admin-facing /api/v2/system endpoints that were
// previously registered in router/admin.go. The systemmetric module still owns
// /system/metrics and /system/metrics/history centrally for now, so this
// registrar only moves the handlers served by module/system itself.
func RoutesAdmin(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceAdmin,
		Name:     "system.admin",
		Register: func(v2 *gin.RouterGroup) {
			system := v2.Group("/system", middleware.JWTAuth(), middleware.RequireSystemRead)
			{
				audit := system.Group("/audit", middleware.RequireAuditRead)
				{
					audit.GET("", handler.ListAuditLogs)
					audit.GET("/:id", handler.GetAuditLog)
				}

				configs := system.Group("/configs")
				{
					configsRead := configs.Group("", middleware.RequireConfigurationRead)
					{
						configsRead.GET("", handler.ListConfigs)
						configsRead.GET("/:config_id", handler.GetConfig)
						configsRead.GET("/:config_id/histories", handler.ListConfigHistories)
					}

					configs.PATCH("/:config_id", middleware.RequireConfigurationUpdate, handler.UpdateConfigValue)
					configs.POST("/:config_id/value/rollback", middleware.RequireConfigurationUpdate, handler.RollbackConfigValue)
					configs.PUT("/:config_id/metadata", middleware.RequireConfigurationConfigure, handler.UpdateConfigMetadata)
					configs.POST("/:config_id/metadata/rollback", middleware.RequireConfigurationConfigure, handler.RollbackConfigMetadata)
				}

				system.GET("/health", handler.GetHealth)

				monitor := system.Group("/monitor")
				monitor.POST("/metrics", handler.GetMetrics)
				monitor.GET("/info", handler.GetSystemInfo)
				monitor.GET("/namespaces/locks", handler.ListNamespaceLocks)
				monitor.GET("/tasks/queue", handler.ListQueuedTasks)
			}
		},
	}
}
