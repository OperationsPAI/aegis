package router

import (
	container "aegis/module/container"
	label "aegis/module/label"
	project "aegis/module/project"
)

type Handlers struct {
	Project   *project.Handler
	Container *container.Handler
	Label     *label.Handler
}

func NewHandlers(
	project *project.Handler,
	container *container.Handler,
	label *label.Handler,
) *Handlers {
	return &Handlers{
		Project:   project,
		Container: container,
		Label:     label,
	}
}
