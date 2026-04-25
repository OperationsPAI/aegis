package app

import (
	chaossystem "aegis/module/chaossystem"
	container "aegis/module/container"
	dataset "aegis/module/dataset"
	execution "aegis/module/execution"
	injection "aegis/module/injection"
	label "aegis/module/label"

	"go.uber.org/fx"
)

//go:generate python3 ../../scripts/generate_http_modules.py

// chaosSystemWriterAdapter bridges chaossystem.Writer (admin-scoped, broad)
// to the narrow injection.ChaosSystemWriter the injection module needs for
// the #156 namespace-count bump. Defined at the app level so the injection
// package can avoid importing chaossystem (which would close the
// chaossystem→initialization→consumer→execution→injection import cycle).
func chaosSystemWriterAdapter(w chaossystem.Writer) injection.ChaosSystemWriter {
	return w
}

func ExecutionInjectionOwnerModules() fx.Option {
	return fx.Options(
		chaossystem.Module,
		container.Module,
		dataset.Module,
		execution.Module,
		injection.Module,
		label.Module,
		fx.Provide(chaosSystemWriterAdapter),
	)
}

func ProducerHTTPModules() fx.Option {
	return fx.Options(
		fx.Options(producerHTTPModules()...),
		fx.Provide(chaosSystemWriterAdapter),
	)
}
