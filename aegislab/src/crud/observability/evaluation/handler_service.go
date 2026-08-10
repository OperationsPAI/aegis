package evaluation

import (
	"context"

	"aegis/platform/authz"
	"aegis/platform/dto"
)

// HandlerService captures evaluation operations consumed by the HTTP handler.
type HandlerService interface {
	ListDatapackEvaluationResults(context.Context, *BatchEvaluateDatapackReq, int) (*BatchEvaluateDatapackResp, error)
	ListDatasetEvaluationResults(context.Context, *BatchEvaluateDatasetReq, int) (*BatchEvaluateDatasetResp, error)
	ListEvaluations(context.Context, authz.CallerScope, *ListEvaluationReq) (*dto.ListResp[EvaluationResp], error)
	GetEvaluation(context.Context, authz.CallerScope, int) (*EvaluationResp, error)
	DeleteEvaluation(context.Context, authz.CallerScope, int) error
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
