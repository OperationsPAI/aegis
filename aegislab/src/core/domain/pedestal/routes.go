package pedestal

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes registers every pedestal endpoint once. /pedestals/* are
// runtime helm operations (list / get / install / restart / uninstall)
// gated by pedestal_read / pedestal_manage. /pedestal/helm/* are the
// helm-config CRUD endpoints — read is open, write needs
// RequireContainerVersionUpload (issue #201).
func Routes(handler *Handler, runtime *RuntimeHandler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "pedestal",
		Register: func(v2 *gin.RouterGroup) {
			pedestals := v2.Group("/pedestals")
			{
				read := pedestals.Group("", middleware.RequirePedestalRead)
				{
					read.GET("", runtime.ListPedestals)
					read.GET("/:release", runtime.GetPedestal)
				}
				manage := pedestals.Group("", middleware.RequirePedestalManage)
				{
					manage.POST("", runtime.InstallPedestal)
					manage.POST("/:release/restart", runtime.RestartPedestal)
					manage.DELETE("/:release", runtime.UninstallPedestal)
				}
			}

			pedestal := v2.Group("/pedestal")
			{
				helm := pedestal.Group("/helm")
				{
					helm.GET("/:container_version_id", handler.GetPedestalHelmConfig)
					helm.POST("/:container_version_id/verify", handler.VerifyPedestalHelmConfig)
					helm.PUT("/:container_version_id", middleware.RequireContainerVersionUpload, handler.UpsertPedestalHelmConfig)
					helm.POST("/:container_version_id/reseed", middleware.RequireContainerVersionUpload, handler.ReseedPedestalHelmConfig)
				}
			}
		},
	}
}
