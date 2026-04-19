package rbac

import "go.uber.org/fx"

var Module = fx.Module("rbac",
	fx.Provide(NewRepository),
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(RoutesAdmin, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
	// Aggregate module-contributed permission rules and role grants
	// (framework fx-groups "permissions" and "role_grants") into
	// consts.SystemRolePermissions so the existing bootstrap readers
	// in service/initialization see them. Phase 3 coexistence: the
	// central map in consts/system.go remains the baseline; Phase 4
	// PRs migrate each module's entries out.
	fx.Invoke(AggregatePermissions),
)
