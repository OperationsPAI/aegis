package runtimeapp

import (
	"aegis/app"
	"aegis/core/orchestrator"

	"go.uber.org/fx"
)

// Options builds the dedicated runtime-worker-service runtime.
//
// The only preserved gRPC seam after phase-2 is runtime-worker →
// api-gateway via the runtime-intake client (configured as either
// `clients.runtime_intake.target` or the legacy
// `runtime_intake.grpc.target`). consumer.RemoteOwnerOptions wires the
// execution / injection owners to that client so state writes never
// touch the runtime-worker-local DB directly — the api-gateway owns
// those records.
func Options(confPath string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.WithSigner(),
		app.ObserveOptions(),
		app.DataOptions(),
		app.CoordinationOptions(),
		app.BuildInfraOptions(),
		app.ExecutionInjectionOwnerModules(),
		app.RuntimeWorkerStackOptions(),
		consumer.RemoteOwnerOptions(),
		app.RequireConfiguredTargets(
			"runtime-worker-service",
			app.RequiredConfigTarget{Name: "api-gateway-intake", PrimaryKey: "clients.runtime_intake.target", LegacyKey: "runtime_intake.grpc.target"},
		),
	)
}
