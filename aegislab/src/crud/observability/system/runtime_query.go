package system

import (
	"context"
	"fmt"

	"aegis/platform/dto"
	systemmetric "aegis/crud/observability/systemmetric"
)

type runtimeQuerySource interface {
	ListNamespaceLocks(context.Context) (*ListNamespaceLockResp, error)
	ListQueuedTasks(context.Context) (*dto.QueuedTasksResp, error)
}

type runtimeQueryAdapter struct {
	local *systemmetric.Service
}

func newRuntimeQuerySource(local *systemmetric.Service) runtimeQuerySource {
	return runtimeQueryAdapter{
		local: local,
	}
}

func (a runtimeQueryAdapter) ListNamespaceLocks(ctx context.Context) (*ListNamespaceLockResp, error) {
	if a.local == nil {
		return nil, fmt.Errorf("runtime query source is not configured")
	}
	return a.local.ListNamespaceLocks(ctx)
}

func (a runtimeQueryAdapter) ListQueuedTasks(ctx context.Context) (*dto.QueuedTasksResp, error) {
	if a.local == nil {
		return nil, fmt.Errorf("runtime query source is not configured")
	}
	return a.local.ListQueuedTasks(ctx)
}
