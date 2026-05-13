package app

import (
	blobclient "aegis/clients/blob"
	chaossystem "aegis/core/domain/chaossystem"
	container "aegis/core/domain/container"
	dataset "aegis/core/domain/dataset"
	execution "aegis/core/domain/execution"
	injection "aegis/core/domain/injection"
	label "aegis/crud/iam/label"
	blob "aegis/crud/storage/blob"

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
		// injection/container/dataset constructors switch on
		// `jfs.backend` and need a blob.Client when backend = "s3".
		// blob.Module provides the in-process Service that LocalClient
		// wraps. Filesystem mode still works without exercising it.
		blob.Module,
		blobclient.Module,
		fx.Provide(chaosSystemWriterAdapter),
	)
}

func ProducerHTTPModules() fx.Option {
	return fx.Options(
		fx.Options(producerHTTPModules()...),
		fx.Provide(chaosSystemWriterAdapter),
	)
}
