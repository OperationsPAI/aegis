package consumer

import (
	"aegis/consts"
	"aegis/framework"

	"go.uber.org/fx"
)

// TaskRegistry is the resolved dispatch table after framework
// aggregation. Populated by NewTaskRegistry which consumes the
// `group:"task_executors"` fx value-group and panics on duplicate
// TaskType registrations.
type TaskRegistry struct {
	executors map[consts.TaskType]framework.TaskExecutor
}

// Lookup returns the TaskExecutor for `t` or (nil, false) if none
// is registered. dispatchTask reports the missing-type error.
func (r *TaskRegistry) Lookup(t consts.TaskType) (framework.TaskExecutor, bool) {
	fn, ok := r.executors[t]
	return fn, ok
}

// TaskRegistryParams collects every module-provided
// TaskExecutorRegistrar. BuiltinTaskExecutors is contributed by
// Module below so the existing 6 executors + the CronJob sentinel are
// always registered.
type TaskRegistryParams struct {
	fx.In

	Contribs []framework.TaskExecutorRegistrar `group:"task_executors"`
}

// NewTaskRegistry flattens every TaskExecutorRegistrar contribution
// into a single dispatch table. Duplicate TaskType registrations
// panic (framework.BuildTaskExecutorRegistry).
func NewTaskRegistry(p TaskRegistryParams) *TaskRegistry {
	return &TaskRegistry{executors: framework.BuildTaskExecutorRegistry(p.Contribs)}
}

// Module provides the consumer-side task-dispatch wiring. It does not
// include the worker lifecycle (that lives in interface/worker) nor the
// owner adapters (those are hand-wired by app/runtime_stack.go).
var Module = fx.Module("consumer",
	fx.Provide(
		NewTaskRegistry,
		fx.Annotate(
			BuiltinTaskExecutors,
			fx.ResultTags(`group:"task_executors"`),
		),
	),
)
