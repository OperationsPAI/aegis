package pedestal

import (
	"context"

	"aegis/model"
	"aegis/service/initialization"
)

// HandlerService captures the pedestal operations consumed by the HTTP handler.
type HandlerService interface {
	GetHelmConfig(context.Context, int) (*model.HelmConfig, error)
	UpsertHelmConfig(context.Context, int, *model.HelmConfig) (*model.HelmConfig, error)
	VerifyHelmConfig(context.Context, int) (*Result, error)
	ReseedHelmConfig(context.Context, ReseedHelmConfigInput) (*initialization.ReseedReport, error)
}

// ReseedHelmConfigInput is the service-layer call shape for the reseed flow.
// Distinct from the HTTP DTO so the service stays free of gin / JSON tags.
type ReseedHelmConfigInput struct {
	ContainerVersionID int
	Env                string
	DataPath           string
	Apply              bool
	Prune              bool
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
