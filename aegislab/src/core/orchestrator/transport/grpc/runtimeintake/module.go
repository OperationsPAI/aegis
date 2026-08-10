// Package grpcruntimeintake serves the runtime-worker → api-gateway
// intake gRPC endpoint (RuntimeIntakeService). It is the only gRPC
// seam preserved after the phase-2 collapse: runtime-worker uses it
// to write execution / injection state back into the shared database
// via the api-gateway-owned modules.
package grpcruntimeintake

import "go.uber.org/fx"

var Module = fx.Module("grpc_runtime_intake",
	fx.Provide(
		newIntakeServer,
		newLifecycle,
	),
	fx.Invoke(registerLifecycle),
)
