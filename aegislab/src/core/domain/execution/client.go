package execution

import (
	"context"

	"aegis/platform/authz"
)

type Reader interface {
	GetExecution(context.Context, authz.CallerScope, int) (*ExecutionDetailResp, error)
	ListEvaluationExecutionsByDatapack(context.Context, *EvaluationExecutionsByDatapackReq) ([]EvaluationExecutionItem, error)
	ListEvaluationExecutionsByDataset(context.Context, *EvaluationExecutionsByDatasetReq) ([]EvaluationExecutionItem, error)
}

type Writer interface {
	CreateExecutionRecord(context.Context, *RuntimeCreateExecutionReq) (int, error)
	UpdateExecutionState(context.Context, *RuntimeUpdateExecutionStateReq) error
}

func AsReader(service *Service) *Service {
	return service
}

func AsWriter(service *Service) *Service {
	return service
}
