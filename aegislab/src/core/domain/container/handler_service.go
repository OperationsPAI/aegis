package container

import (
	"context"

	"aegis/platform/authz"
	"aegis/platform/dto"

	"mime/multipart"
)

// HandlerService captures the container operations consumed by the HTTP handler.
type HandlerService interface {
	CreateContainer(context.Context, *CreateContainerReq, int) (*ContainerResp, error)
	RegisterContainer(context.Context, *RegisterContainerReq, int) (*RegisterContainerResp, error)
	DeleteContainer(context.Context, authz.CallerScope, int) error
	GetContainer(context.Context, authz.CallerScope, int) (*ContainerDetailResp, error)
	ListContainers(context.Context, authz.CallerScope, *ListContainerReq) (*dto.ListResp[ContainerResp], error)
	UpdateContainer(context.Context, authz.CallerScope, *UpdateContainerReq, int) (*ContainerResp, error)
	ManageContainerLabels(context.Context, authz.CallerScope, *ManageContainerLabelReq, int) (*ContainerResp, error)
	CreateContainerVersion(context.Context, authz.CallerScope, *CreateContainerVersionReq, int, int) (*ContainerVersionResp, error)
	DeleteContainerVersion(context.Context, authz.CallerScope, int) error
	GetContainerVersion(context.Context, authz.CallerScope, int, int) (*ContainerVersionDetailResp, error)
	ListContainerVersions(context.Context, authz.CallerScope, *ListContainerVersionReq, int) (*dto.ListResp[ContainerVersionResp], error)
	UpdateContainerVersion(context.Context, authz.CallerScope, *UpdateContainerVersionReq, int, int) (*ContainerVersionResp, error)
	SetContainerVersionImage(context.Context, authz.CallerScope, *SetContainerVersionImageReq, int) (*SetContainerVersionImageResp, error)
	SubmitContainerBuilding(context.Context, *SubmitBuildContainerReq, string, int) (*SubmitContainerBuildResp, error)
	UploadHelmChart(context.Context, authz.CallerScope, *multipart.FileHeader, int, int, int) (*UploadHelmChartResp, error)
	UploadHelmValueFile(context.Context, authz.CallerScope, *multipart.FileHeader, int, int, int) (*UploadHelmValueFileResp, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
