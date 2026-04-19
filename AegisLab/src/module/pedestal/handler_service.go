package pedestal

import (
	"context"

	"aegis/model"
)

// HandlerService captures the pedestal operations consumed by the HTTP handler.
type HandlerService interface {
	GetHelmConfig(context.Context, int) (*model.HelmConfig, error)
	UpsertHelmConfig(context.Context, int, *model.HelmConfig) (*model.HelmConfig, error)
	VerifyHelmConfig(context.Context, int) (*Result, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
