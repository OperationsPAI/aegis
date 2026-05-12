package monolith

import (
	"aegis/app"
	chaos "aegis/infra/chaos"
	k8s "aegis/infra/k8s"
	grpcruntimeintake "aegis/interface/grpc/runtimeintake"
	rbac "aegis/module/rbac"

	"go.uber.org/fx"
)

// Options builds the monolith API runtime (cmd/aegis-api).
//
// This is the single API binary: it owns every module and wires them
// via plain local provides (no fx.Decorate remote-shim layer). The only
// gRPC surface it exposes is the RuntimeIntakeService, which
// runtime-worker uses to write execution and injection state back into
// the shared DB.
//
// Note: this used to live at app/gateway / cmd/api-gateway. Despite the
// historical name it was always the monolith; the real L7 gateway lives
// at app/gateway + cmd/aegis-gateway (see docs/rfcs/api-gateway.md).
func Options(confPath, port string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.WithSigner(),
		app.ObserveOptions(),
		app.DataOptions(),
		app.CoordinationOptions(),
		app.BuildInfraOptions(),
		chaos.Module,
		k8s.Module,
		app.ProducerHTTPOptions(port),
		rbac.Module,
		grpcruntimeintake.Module,
	)
}
