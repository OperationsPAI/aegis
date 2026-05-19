package injection

import (
	"archive/zip"
	"context"
	"io"

	"aegis/platform/authz"
	"aegis/platform/dto"
	"aegis/platform/utils"
)

// HandlerService captures the injection operations consumed by the HTTP handler.
type HandlerService interface {
	ListProjectInjections(context.Context, authz.CallerScope, *ListInjectionReq, int) (*dto.ListResp[InjectionResp], error)
	Search(context.Context, authz.CallerScope, *SearchInjectionReq, *int) (*dto.SearchResp[InjectionDetailResp], error)
	ListNoIssues(context.Context, authz.CallerScope, *ListInjectionNoIssuesReq, *int) ([]InjectionNoIssuesResp, error)
	ListWithIssues(context.Context, authz.CallerScope, *ListInjectionWithIssuesReq, *int) ([]InjectionWithIssuesResp, error)
	SubmitFaultInjection(context.Context, *SubmitInjectionReq, string, int, *int) (*SubmitInjectionResp, error)
	SubmitDatapackBuilding(context.Context, *SubmitDatapackBuildingReq, string, int, *int) (*SubmitDatapackBuildingResp, error)
	ListInjections(context.Context, authz.CallerScope, *ListInjectionReq) (*dto.ListResp[InjectionResp], error)
	GetInjection(context.Context, authz.CallerScope, int) (*InjectionDetailResp, error)
	ManageLabels(context.Context, authz.CallerScope, *ManageInjectionLabelReq, int) (*InjectionResp, error)
	BatchManageLabels(context.Context, authz.CallerScope, *BatchManageInjectionLabelReq) (*BatchManageInjectionLabelResp, error)
	BatchDelete(context.Context, authz.CallerScope, *BatchDeleteInjectionReq) error
	Clone(context.Context, authz.CallerScope, int, *CloneInjectionReq) (*InjectionDetailResp, error)
	GetLogs(context.Context, authz.CallerScope, int) (*InjectionLogsResp, error)
	GetLogsFiltered(context.Context, int, *InjectionLogQueryReq) (*InjectionLogsFilteredResp, error)
	GetLogsHistogram(context.Context, int, *InjectionLogHistogramReq) (*InjectionLogHistogramResp, error)
	GetTimeline(context.Context, int) (*InjectionTimelineResp, error)
	GetDatapackFilename(context.Context, authz.CallerScope, int) (string, error)
	DownloadDatapack(context.Context, authz.CallerScope, *zip.Writer, []utils.ExculdeRule, int) error
	GetDatapackFiles(context.Context, authz.CallerScope, int, string) (*DatapackFilesResp, error)
	DownloadDatapackFile(context.Context, authz.CallerScope, int, string) (string, string, int64, io.ReadSeekCloser, error)
	QueryDatapackFile(context.Context, authz.CallerScope, int, string) (string, int64, io.ReadCloser, error)
	GetDatapackSchema(context.Context, authz.CallerScope, int) (*DatapackSchemaResp, error)
	QueryDatapack(context.Context, authz.CallerScope, int, string) (io.ReadCloser, error)
	DiagnoseDatapack(context.Context, authz.CallerScope, int) (*DatapackDiagnoseResp, error)
	UpdateGroundtruth(context.Context, authz.CallerScope, int, *UpdateGroundtruthReq) error
	UploadDatapack(context.Context, authz.CallerScope, *UploadDatapackReq, io.Reader, int64) (*UploadDatapackResp, error)
	CancelInjection(context.Context, authz.CallerScope, int) (*CancelInjectionResp, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
