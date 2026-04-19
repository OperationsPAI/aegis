package router

import (
	"aegis/framework"
	auth "aegis/module/auth"
	container "aegis/module/container"
	dataset "aegis/module/dataset"
	evaluation "aegis/module/evaluation"
	execution "aegis/module/execution"
	group "aegis/module/group"
	injection "aegis/module/injection"
	label "aegis/module/label"
	metric "aegis/module/metric"
	project "aegis/module/project"
	ratelimiter "aegis/module/ratelimiter"
	task "aegis/module/task"
)

type Handlers struct {
	Auth        *auth.Handler
	Project     *project.Handler
	Task        *task.Handler
	Injection   *injection.Handler
	Execution   *execution.Handler
	Container   *container.Handler
	Dataset     *dataset.Handler
	Evaluation  *evaluation.Handler
	Group       *group.Handler
	Metric      *metric.Handler
	SDK         framework.SDKRoutesHandler
	RateLimiter *ratelimiter.Handler
	Label       *label.Handler
}

func NewHandlers(
	auth *auth.Handler,
	project *project.Handler,
	task *task.Handler,
	injection *injection.Handler,
	execution *execution.Handler,
	container *container.Handler,
	dataset *dataset.Handler,
	evaluation *evaluation.Handler,
	group *group.Handler,
	metric *metric.Handler,
	sdk framework.SDKRoutesHandler,
	rateLimiter *ratelimiter.Handler,
	label *label.Handler,
) *Handlers {
	return &Handlers{
		Auth:        auth,
		Project:     project,
		Task:        task,
		Injection:   injection,
		Execution:   execution,
		Container:   container,
		Dataset:     dataset,
		Evaluation:  evaluation,
		Group:       group,
		Metric:      metric,
		SDK:         sdk,
		RateLimiter: rateLimiter,
		Label:       label,
	}
}
