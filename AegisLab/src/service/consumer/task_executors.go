package consumer

import (
	"context"
	"fmt"

	"aegis/consts"
	"aegis/dto"
	"aegis/framework"
)

// BuiltinTaskExecutors returns the registry fragment that holds every
// executor that today lives in service/consumer/*.go. It is contributed
// into the `group:"task_executors"` fx value-group by
// service/consumer.Module so existing dispatch behavior is preserved
// without requiring every executor to move into a per-TaskType module
// in Phase 3.
//
// Each wrapper type-asserts the framework-opaque `deps any` back to
// RuntimeDeps, then calls the original execute* function unchanged.
// Phase 4 PRs that move an executor into a TaskType-owning module (for
// example, a future module/faultinjection) will remove its entry from
// this registrar and contribute its own registrar.
//
// TaskTypeCronJob is deliberately a sentinel: the scheduler re-enqueues
// cron tasks by resubmitting them with one of the regular TaskType
// values (see service/common/task.go), so the dispatcher should never
// see a TaskTypeCronJob. We register it to guarantee that if one ever
// slips through, the error message says WHY instead of the generic
// "unknown task type" — which used to be the pre-existing bug from
// the original refactor (the switch had no case for CronJob and fell
// through to "unknown task type: 6").
func BuiltinTaskExecutors() framework.TaskExecutorRegistrar {
	return framework.TaskExecutorRegistrar{
		Module: "consumer.builtin",
		Executors: map[consts.TaskType]framework.TaskExecutor{
			consts.TaskTypeBuildContainer:  wrapExecutor(executeBuildContainer),
			consts.TaskTypeRestartPedestal: wrapExecutor(executeRestartPedestal),
			consts.TaskTypeFaultInjection:  wrapExecutor(executeFaultInjection),
			consts.TaskTypeBuildDatapack:   wrapExecutor(executeBuildDatapackWithDeps),
			consts.TaskTypeRunAlgorithm:    wrapExecutor(executeAlgorithm),
			consts.TaskTypeCollectResult:   wrapExecutor(executeCollectResult),
			consts.TaskTypeCronJob:         cronJobSentinel,
		},
	}
}

// wrapExecutor adapts a RuntimeDeps-typed executor to the framework's
// deps-as-any signature.
func wrapExecutor(fn func(context.Context, *dto.UnifiedTask, RuntimeDeps) error) framework.TaskExecutor {
	return func(ctx context.Context, task *dto.UnifiedTask, deps any) error {
		runtime, ok := deps.(RuntimeDeps)
		if !ok {
			return fmt.Errorf("task-executor dispatch: deps is %T, want consumer.RuntimeDeps", deps)
		}
		return fn(ctx, task, runtime)
	}
}

// cronJobSentinel explicitly errors on a TaskTypeCronJob reaching the
// dispatcher. No real cron executor exists — cron tasks get rescheduled
// by the scheduler loop (service/common/task.go) and the next execution
// is submitted under its real TaskType. If you're hitting this, the
// scheduler likely enqueued a cron task on the ready queue by mistake;
// debug service/common.SubmitTask and calculateExecuteTime rather than
// adding a handler here.
func cronJobSentinel(ctx context.Context, task *dto.UnifiedTask, deps any) error {
	return fmt.Errorf("consumer: TaskTypeCronJob should be scheduler-only; task %q reached the dispatcher — this is a scheduler bug, not a missing executor", task.TaskID)
}
