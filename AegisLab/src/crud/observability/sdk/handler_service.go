package sdk

import (
	"context"

	"aegis/platform/dto"
)

// HandlerService captures the SDK operations consumed by the HTTP handler.
type HandlerService interface {
	GetEvaluation(context.Context, int) (*SDKEvaluationSample, error)
	ListDatasetSamples(context.Context, *ListSDKDatasetSampleReq) (*dto.ListResp[SDKDatasetSample], error)
	ListEvaluations(context.Context, *ListSDKEvaluationReq) (*dto.ListResp[SDKEvaluationSample], error)
	ListExperiments(context.Context) (*SDKExperimentListResp, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
