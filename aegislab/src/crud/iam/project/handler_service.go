package project

import (
	"context"

	"aegis/platform/authz"
	"aegis/platform/dto"
)

// HandlerService captures the project operations consumed by the HTTP handler.
type HandlerService interface {
	CreateProject(context.Context, *CreateProjectReq, int) (*ProjectResp, error)
	DeleteProject(context.Context, authz.CallerScope, int) error
	GetProjectDetail(context.Context, authz.CallerScope, int) (*ProjectDetailResp, error)
	ListProjects(context.Context, authz.CallerScope, *ListProjectReq) (*dto.ListResp[ProjectResp], error)
	UpdateProject(context.Context, authz.CallerScope, *UpdateProjectReq, int) (*ProjectResp, error)
	ManageProjectLabels(context.Context, authz.CallerScope, *ManageProjectLabelReq, int) (*ProjectResp, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
