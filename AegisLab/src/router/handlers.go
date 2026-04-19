package router

import (
	"aegis/framework"
	auth "aegis/module/auth"
	container "aegis/module/container"
	evaluation "aegis/module/evaluation"
	execution "aegis/module/execution"
	injection "aegis/module/injection"
	label "aegis/module/label"
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
	Dataset     framework.DatasetHandler
	Evaluation  *evaluation.Handler
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
	dataset framework.DatasetHandler,
	evaluation *evaluation.Handler,
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
		RateLimiter: rateLimiter,
		Label:       label,
	}
}
