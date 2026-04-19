package rbac

import "go.uber.org/fx"

var Module = fx.Module("rbac",
	fx.Provide(NewRepository),
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	// Aggregate module-contributed permission rules and role grants
	// (framework fx-groups "permissions" and "role_grants") into
	// consts.SystemRolePermissions so the existing bootstrap readers
	// in service/initialization see them. Phase 3 coexistence: the
	// central map in consts/system.go remains the baseline; Phase 4
	// PRs migrate each module's entries out.
	fx.Invoke(AggregatePermissions),
)
