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


	// /api/v2/labels routes moved to module/label/routes.go (Phase 3
	// reference migration). The label module self-registers via
	// framework.RouteRegistrar; see AegisLab/CONTRIBUTING.md.
}
