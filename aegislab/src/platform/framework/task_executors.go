package framework

import (
	"context"
	"fmt"

	"aegis/platform/consts"
	"aegis/platform/dto"
)

// TaskExecutor is the function signature that every TaskType-specific
// handler must implement. It is framework-agnostic — the concrete
// RuntimeDeps struct lives in service/consumer and is passed through as
// an opaque value via the dispatcher that assembles the registry.
//
// We use `any` for deps here (rather than service/consumer.RuntimeDeps)
// because importing service/consumer from the framework would create an
// import cycle: module/<name>/task_executors.go imports framework, and
// service/consumer needs to register the dispatcher that uses framework
// to look up executors. The consumer-side wrapper in
// service/consumer/distribute_tasks.go type-asserts deps back to its
// concrete type.
type TaskExecutor func(ctx context.Context, task *dto.UnifiedTask, deps any) error

// TaskExecutorRegistrar is what a module contributes for task-type
// self-registration. `Executors` is a map fragment keyed by TaskType;
// the aggregator merges every fragment into a single registry at startup.
// Duplicate TaskType registrations panic — there can only be one
// dispatcher per type.
type TaskExecutorRegistrar struct {
	Module    string
	Executors map[consts.TaskType]TaskExecutor
}

// BuildTaskExecutorRegistry folds every contribution into a single map.
// Duplicate TaskType across contributions is a programmer error — we
// panic because it's unrecoverable at startup.
func BuildTaskExecutorRegistry(contribs []TaskExecutorRegistrar) map[consts.TaskType]TaskExecutor {
	out := make(map[consts.TaskType]TaskExecutor)
	for _, c := range contribs {
		for t, fn := range c.Executors {
			if _, exists := out[t]; exists {
				panic(fmt.Sprintf("framework: duplicate TaskExecutor registered for TaskType=%d (conflicting module=%q)", t, c.Module))
			}
			out[t] = fn
		}
	}
	return out
}
