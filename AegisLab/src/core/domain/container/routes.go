package container

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "container.portal",
		Register: func(v2 *gin.RouterGroup) {
			containers := v2.Group("/containers", middleware.JWTAuth())
			{
				containerRead := containers.Group("", middleware.RequireContainerRead)
				{
					containerRead.GET("", handler.ListContainers)
					containerRead.GET("/:container_id", handler.GetContainer)
				}

				containers.POST("", middleware.RequireContainerCreate, handler.CreateContainer)
				containers.PATCH("/:container_id", middleware.RequireContainerUpdate, handler.UpdateContainer)
				containers.PATCH("/:container_id/labels", middleware.RequireContainerUpdate, handler.ManageContainerCustomLabels)
				containers.DELETE("/:container_id", middleware.RequireContainerDelete, handler.DeleteContainer)
				containers.POST("/build", middleware.RequireContainerExecute, handler.SubmitContainerBuilding)
				// Atomic register: creates (container, container_version,
				// helm_config) in one transaction with stage-tagged logs
				// correlated by register_id (issue #102).
				containers.POST("/register", middleware.RequireContainerCreate, handler.RegisterContainer)

				containerVersions := containers.Group("/:container_id/versions")
				{
					containerVersionRead := containerVersions.Group("", middleware.RequireContainerVersionRead)
					{
						containerVersionRead.GET("", handler.ListContainerVersions)
						containerVersionRead.GET("/:version_id", handler.GetContainerVersion)
					}

					containerVersions.POST("", middleware.RequireContainerVersionCreate, handler.CreateContainerVersion)
					containerVersions.PATCH("/:version_id", middleware.RequireContainerVersionUpdate, handler.UpdateContainerVersion)
					containerVersions.DELETE("/:version_id", middleware.RequireContainerVersionDelete, handler.DeleteContainerVersion)
					containerVersions.POST("/:version_id/helm-chart", middleware.RequireContainerVersionUpload, handler.UploadHelmChart)
					containerVersions.POST("/:version_id/helm-values", middleware.RequireContainerVersionUpload, handler.UploadHelmValueFile)
				}
			}

			flatContainerVersions := v2.Group("/container-versions", middleware.JWTAuth())
			{
				flatContainerVersions.PATCH("/:id/image", middleware.RequireContainerVersionUpdate, handler.SetContainerVersionImage)
			}
		},
	}
}
