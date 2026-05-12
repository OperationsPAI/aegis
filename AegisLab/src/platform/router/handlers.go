package router

import (
	container "aegis/module/container"
	label "aegis/crud/iam/label"
	project "aegis/crud/iam/project"
	sdk "aegis/crud/observability/sdk"
)

type Handlers struct {
	Project   *project.Handler
	Container *container.Handler
	SDK       *sdk.Handler
	Label     *label.Handler
}

func NewHandlers(
	project *project.Handler,
	container *container.Handler,
	sdk *sdk.Handler,
	label *label.Handler,
) *Handlers {
	return &Handlers{
		Project:   project,
		Container: container,
		SDK:       sdk,
		Label:     label,
	}
}
