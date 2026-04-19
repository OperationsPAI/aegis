package router

import (
	auth "aegis/module/auth"
	container "aegis/module/container"
	evaluation "aegis/module/evaluation"
	execution "aegis/module/execution"
	injection "aegis/module/injection"
	label "aegis/module/label"
	project "aegis/module/project"
)

type Handlers struct {
	Auth       *auth.Handler
	Project    *project.Handler
	Injection  *injection.Handler
	Execution  *execution.Handler
	Container  *container.Handler
	Evaluation *evaluation.Handler
	Label      *label.Handler
}

func NewHandlers(
	auth *auth.Handler,
	project *project.Handler,
	injection *injection.Handler,
	execution *execution.Handler,
	container *container.Handler,
	evaluation *evaluation.Handler,
	label *label.Handler,
) *Handlers {
	return &Handlers{
		Auth:       auth,
		Project:    project,
		Injection:  injection,
		Execution:  execution,
		Container:  container,
		Evaluation: evaluation,
		Label:      label,
	}
}
