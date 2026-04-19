package gateway

import (
	"aegis/app"
	chaos "aegis/infra/chaos"
	k8s "aegis/infra/k8s"
	grpcruntimeintake "aegis/interface/grpc/runtimeintake"

	"go.uber.org/fx"
)

// Options builds the dedicated api-gateway runtime.
//
// Post phase-2, api-gateway is the single API binary: it owns every
// module and wires them via plain local provides (no fx.Decorate
// remote-shim layer). The only gRPC surface it exposes is the
// RuntimeIntakeService, which runtime-worker uses to write execution
// and injection state back into the shared DB.
func Options(confPath, port string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.ObserveOptions(),
		app.DataOptions(),
		app.CoordinationOptions(),
		app.BuildInfraOptions(),
		chaos.Module,
		k8s.Module,
		app.ProducerHTTPOptions(port),
		grpcruntimeintake.Module,
	)
}
