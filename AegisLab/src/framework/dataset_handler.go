package framework

import "github.com/gin-gonic/gin"

// DatasetHandler is the router-facing surface for dataset HTTP endpoints.
// Declaring it in framework lets router depend on the contract without
// importing module/dataset directly.
type DatasetHandler interface {
	CreateDataset(*gin.Context)
	DeleteDataset(*gin.Context)
	GetDataset(*gin.Context)
	ListDatasets(*gin.Context)
	SearchDataset(*gin.Context)
	UpdateDataset(*gin.Context)
	ManageDatasetCustomLabels(*gin.Context)
	CreateDatasetVersion(*gin.Context)
	DeleteDatasetVersion(*gin.Context)
	GetDatasetVersion(*gin.Context)
	ListDatasetVersions(*gin.Context)
	UpdateDatasetVersion(*gin.Context)
	DownloadDatasetVersion(*gin.Context)
	ManageDatasetVersionInjections(*gin.Context)
}
