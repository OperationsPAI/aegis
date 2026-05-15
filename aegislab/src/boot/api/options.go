package api

import (
	"aegis/boot"
	grpcruntimeintake "aegis/core/orchestrator/transport/grpc/runtimeintake"
	rbac "aegis/crud/iam/rbac"
	chaos "aegis/platform/chaos"
	k8s "aegis/platform/k8s"
	"aegis/platform/middleware"

	"go.uber.org/fx"
)

// Options builds the aegis-api business runtime (cmd/aegis-api).
//
// aegis-api is the *business* API process — chaos injection, executions,
// datasets, containers, tasks, etc. Identity (sso), notifications (notify),
// blob storage (blob), config-center, and the L7 gateway are separate
// binaries that this process talks to through clients/sso, clients/blob,
// clients/notification, clients/configcenter.
//
// The only gRPC surface aegis-api itself exposes is the RuntimeIntakeService,
// which runtime-worker uses to write execution / injection state back into
// the shared DB.
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
		// rbac stays here (not in monolithHTTPModules) because aegis-api
		// must contribute its module-internal RoleGrantsRegistrar /
		// PermissionRegistrar to AggregatePermissions so the every-boot
		// ReconcileSystemPermissions seed sees them. The rbac HTTP routes
		// themselves are served by aegis-sso; this import is for the
		// fx-group registrars + the AggregatePermissions invoke only.
		rbac.Module,
		grpcruntimeintake.Module,
		fx.Invoke(func() { middleware.AssertTrustedHeaderKeyConfigured() }),
	)
}
