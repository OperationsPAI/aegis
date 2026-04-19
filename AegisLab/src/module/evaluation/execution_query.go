package evaluation

import (
	"context"
	"fmt"

	execution "aegis/module/execution"
)

type executionQuerySource interface {
	ListEvaluationExecutionsByDatapack(context.Context, *execution.EvaluationExecutionsByDatapackReq) ([]execution.EvaluationExecutionItem, error)
	ListEvaluationExecutionsByDataset(context.Context, *execution.EvaluationExecutionsByDatasetReq) ([]execution.EvaluationExecutionItem, error)
}

type executionQueryAdapter struct {
	local *execution.Service
}

func newExecutionQuerySource(local *execution.Service) executionQuerySource {
	return executionQueryAdapter{
		local: local,
	}
}

func (a executionQueryAdapter) ListEvaluationExecutionsByDatapack(ctx context.Context, req *execution.EvaluationExecutionsByDatapackReq) ([]execution.EvaluationExecutionItem, error) {
	if a.local == nil {
		return nil, fmt.Errorf("evaluation execution query source is not configured")
	}
	return a.local.ListEvaluationExecutionsByDatapack(ctx, req)
}

func (a executionQueryAdapter) ListEvaluationExecutionsByDataset(ctx context.Context, req *execution.EvaluationExecutionsByDatasetReq) ([]execution.EvaluationExecutionItem, error) {
	if a.local == nil {
		return nil, fmt.Errorf("evaluation execution query source is not configured")
	}
	return a.local.ListEvaluationExecutionsByDataset(ctx, req)
}
