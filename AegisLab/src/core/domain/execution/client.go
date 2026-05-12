package execution

import "context"

type Reader interface {
	GetExecution(context.Context, int) (*ExecutionDetailResp, error)
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
