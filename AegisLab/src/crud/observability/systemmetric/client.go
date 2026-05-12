package systemmetric

import (
	"context"

	task "aegis/core/domain/task"
)

// Reader exposes the system metric data other modules currently consume.
// Phase 4 follow-up PRs can switch from the concrete Service dependency
// to this interface without widening the dependency surface.
type Reader interface {
	ListNamespaceLocks(context.Context) (*ListNamespaceLockResp, error)
	ListQueuedTasks(context.Context) (*task.QueuedTasksResp, error)
}

func AsReader(service *Service) Reader {
	return service
}
