package router

import (
	auth "aegis/module/auth"
	container "aegis/module/container"
	label "aegis/module/label"
	project "aegis/module/project"
)

type Handlers struct {
	Auth      *auth.Handler
	Project   *project.Handler
	Container *container.Handler
	Label     *label.Handler
}

func NewHandlers(
	auth *auth.Handler,
	project *project.Handler,
	container *container.Handler,
	label *label.Handler,
) *Handlers {
	return &Handlers{
		Auth:      auth,
		Project:   project,
		Container: container,
		Label:     label,
	}
}
