// Package chaosprune is the admin/operator API surface for finding and
// deleting orphaned chaos-mesh CRs — CRs whose backing injection task has
// terminated (or whose task no longer exists). Parallel safety-net to the
// per-namespace NamespaceReclaimer / RestartPedestal cleanup.
package chaosprune

import "go.uber.org/fx"

var Module = fx.Module("chaosprune",
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(Routes, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
