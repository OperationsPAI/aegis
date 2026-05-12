package chaossystem

import (
	"context"

	"aegis/platform/dto"
	"aegis/service/initialization"
)

// HandlerService captures chaos system operations consumed by HTTP and resource gRPC handlers.
type HandlerService interface {
	ListSystems(context.Context, *ListChaosSystemReq) (*dto.ListResp[ChaosSystemResp], error)
	GetSystem(context.Context, int) (*ChaosSystemResp, error)
	GetSystemChart(ctx context.Context, name, version string) (*SystemChartResp, error)
	CreateSystem(context.Context, *CreateChaosSystemReq) (*ChaosSystemResp, error)
	UpdateSystem(context.Context, int, *UpdateChaosSystemReq) (*ChaosSystemResp, error)
	DeleteSystem(context.Context, int) error
	UpsertMetadata(context.Context, int, *BulkUpsertSystemMetadataReq) error
	ListMetadata(context.Context, int, string) ([]SystemMetadataResp, error)
	ReseedSystems(context.Context, *ReseedSystemReq) (*initialization.ReseedReport, error)
	ListPrerequisites(context.Context, string) ([]SystemPrerequisiteResp, error)
	MarkPrerequisite(context.Context, string, int, *MarkPrerequisiteReq) (*SystemPrerequisiteResp, error)
	ListInjectCandidates(context.Context, string, string) (*InjectCandidatesResp, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
