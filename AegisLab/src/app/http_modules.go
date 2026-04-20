package app

import (
	container "aegis/module/container"
	dataset "aegis/module/dataset"
	execution "aegis/module/execution"
	injection "aegis/module/injection"
	label "aegis/module/label"

	"go.uber.org/fx"
)

//go:generate python3 ../../scripts/generate_http_modules.py

func ExecutionInjectionOwnerModules() fx.Option {
	return fx.Options(
		container.Module,
		dataset.Module,
		execution.Module,
		injection.Module,
		label.Module,
	)
}

func ProducerHTTPModules() fx.Option {
	return fx.Options(producerHTTPModules()...)
}
