package dataset

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "dataset.portal",
		Register: func(v2 *gin.RouterGroup) {
			datasets := v2.Group("/datasets", middleware.JWTAuth())
			{
				datasetRead := datasets.Group("", middleware.RequireDatasetRead)
				{
					datasetRead.GET("", handler.ListDatasets)
					datasetRead.GET("/:dataset_id", handler.GetDataset)
					datasetRead.POST("/search", handler.SearchDataset)
				}

				datasets.POST("", middleware.RequireDatasetCreate, handler.CreateDataset)
				datasets.PATCH("/:dataset_id", middleware.RequireDatasetUpdate, handler.UpdateDataset)
				datasets.PATCH("/:dataset_id/labels", middleware.RequireDatasetUpdate, handler.ManageDatasetCustomLabels)
				datasets.DELETE("/:dataset_id", middleware.RequireDatasetDelete, handler.DeleteDataset)

				datasetVersions := datasets.Group("/:dataset_id/versions")
				{
					datasetVersionRead := datasetVersions.Group("", middleware.RequireDatasetVersionRead)
					{
						datasetVersionRead.GET("", handler.ListDatasetVersions)
						datasetVersionRead.GET("/:version_id", handler.GetDatasetVersion)
					}

					datasetVersions.POST("", middleware.RequireDatasetVersionCreate, handler.CreateDatasetVersion)
					datasetVersions.PATCH("/:version_id", middleware.RequireDatasetVersionUpdate, handler.UpdateDatasetVersion)
					datasetVersions.DELETE("/:version_id", middleware.RequireDatasetVersionDelete, handler.DeleteDatasetVersion)
				}
			}
		},
	}
}

func RoutesSDK(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "dataset.sdk",
		Register: func(v2 *gin.RouterGroup) {
			datasets := v2.Group("/datasets", middleware.JWTAuth())
			{
				datasetVersions := datasets.Group("/:dataset_id/versions")
				{
					datasetVersions.GET(
						"/:version_id/download",
						middleware.RequireDatasetVersionDownload,
						middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKDatasetsAll, consts.ScopeSDKDatasetsRead),
						handler.DownloadDatasetVersion,
					)
				}

				datasets.PATCH(
					"/:dataset_id/version/:version_id/injections",
					middleware.RequireDatasetVersionUpdate,
					middleware.RequireAPIKeyScopesAny(consts.ScopeSDKAll, consts.ScopeSDKDatasetsAll, consts.ScopeSDKDatasetsWrite),
					handler.ManageDatasetVersionInjections,
				)
			}
		},
	}
}
