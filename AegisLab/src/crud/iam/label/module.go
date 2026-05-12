package label

import "go.uber.org/fx"

var Module = fx.Module("label",
	fx.Provide(NewRepository),
	fx.Provide(AsWriter),
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	// Phase 3 self-registration: this module contributes its routes,
	// permissions, role grants, and migrations via fx-groups so the
	// aggregation sites (router.New, module/rbac, infra/db) don't need
	// to be edited when future modules are added. See
	// AegisLab/CONTRIBUTING.md for the Phase 4 pattern.
	fx.Provide(
		fx.Annotate(Routes, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
