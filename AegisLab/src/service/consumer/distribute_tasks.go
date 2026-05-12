package consumer

import (
	"context"
	"fmt"
	"runtime/debug"

	"aegis/consts"
	"aegis/dto"
	"aegis/infra/tracing"

	"github.com/sirupsen/logrus"
)

// dispatchTask looks up the TaskExecutor for task.Type in the framework-
// aggregated registry (deps.TaskRegistry) and delegates to it. The
// pre-Phase-3 switch statement is gone; Phase 4 modules that own a
// TaskType contribute their executor via fx-group `task_executors`.
//
// The 6 original executors still live in this package and are contributed
// by consumer.BuiltinTaskExecutors (see module.go). The CronJob sentinel
// is also registered there — see task_executors.go for rationale.
func dispatchTask(ctx context.Context, task *dto.UnifiedTask, deps RuntimeDeps) error {
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("Task panic: %v\n%s", r, debug.Stack())
		}
	}()

	tracing.SetSpanAttribute(ctx, consts.TaskIDKey, task.TaskID)
	tracing.SetSpanAttribute(ctx, consts.TaskTypeKey, consts.GetTaskTypeName(task.Type))
	tracing.SetSpanAttribute(ctx, consts.TaskStateKey, consts.GetTaskStateName(consts.TaskPending))

	publishEvent(deps.RedisGateway, ctx, fmt.Sprintf(consts.StreamTraceLogKey, task.TraceID), dto.TraceStreamEvent{
		TaskID:    task.TaskID,
		TaskType:  task.Type,
		EventName: consts.EventTaskStarted,
		Payload:   task,
	})

	if deps.TaskRegistry == nil {
		return fmt.Errorf("task-dispatch: RuntimeDeps.TaskRegistry is nil — consumer.Module not wired")
	}

	executor, ok := deps.TaskRegistry.Lookup(task.Type)
	if !ok {
		return fmt.Errorf("unknown task type: %d", task.Type)
	}

	return executor(ctx, task, deps)
}
