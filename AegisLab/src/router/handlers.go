package router

import (
	auth "aegis/module/auth"
	container "aegis/module/container"
	evaluation "aegis/module/evaluation"
	execution "aegis/module/execution"
	label "aegis/module/label"
	project "aegis/module/project"
)

type Handlers struct {
	Auth       *auth.Handler
	Project    *project.Handler
	Execution  *execution.Handler
	Container  *container.Handler
	Evaluation *evaluation.Handler
	Label      *label.Handler
}

func NewHandlers(
	auth *auth.Handler,
	project *project.Handler,
	execution *execution.Handler,
	container *container.Handler,
	evaluation *evaluation.Handler,
	label *label.Handler,
) *Handlers {
	return &Handlers{
		Auth:       auth,
		Project:    project,
		Execution:  execution,
		Container:  container,
		Evaluation: evaluation,
		Label:      label,
	}
}
