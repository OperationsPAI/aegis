package dataset

import (
	"archive/zip"
	"context"

	"aegis/platform/authz"
	"aegis/platform/dto"
	"aegis/platform/utils"
)

// HandlerService captures the dataset operations consumed by the HTTP handler.
type HandlerService interface {
	CreateDataset(context.Context, *CreateDatasetReq, int) (*DatasetResp, error)
	DeleteDataset(context.Context, authz.CallerScope, int) error
	GetDataset(context.Context, authz.CallerScope, int) (*DatasetDetailResp, error)
	ListDatasets(context.Context, authz.CallerScope, *ListDatasetReq) (*dto.ListResp[DatasetResp], error)
	SearchDatasets(context.Context, authz.CallerScope, *SearchDatasetReq) (*dto.ListResp[DatasetDetailResp], error)
	UpdateDataset(context.Context, authz.CallerScope, *UpdateDatasetReq, int) (*DatasetResp, error)
	ManageDatasetLabels(context.Context, authz.CallerScope, *ManageDatasetLabelReq, int) (*DatasetResp, error)
	CreateDatasetVersion(context.Context, authz.CallerScope, *CreateDatasetVersionReq, int, int) (*DatasetVersionResp, error)
	DeleteDatasetVersion(context.Context, authz.CallerScope, int) error
	GetDatasetVersion(context.Context, authz.CallerScope, int, int) (*DatasetVersionDetailResp, error)
	ListDatasetVersions(context.Context, authz.CallerScope, *ListDatasetVersionReq, int) (*dto.ListResp[DatasetVersionResp], error)
	UpdateDatasetVersion(context.Context, authz.CallerScope, *UpdateDatasetVersionReq, int, int) (*DatasetVersionResp, error)
	GetDatasetVersionFilename(context.Context, authz.CallerScope, int, int) (string, error)
	DownloadDatasetVersion(context.Context, authz.CallerScope, *zip.Writer, []utils.ExculdeRule, int) error
	ManageDatasetVersionInjections(context.Context, authz.CallerScope, *ManageDatasetVersionInjectionReq, int) (*DatasetVersionDetailResp, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
