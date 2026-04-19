package system

import "go.uber.org/fx"

var Module = fx.Module("system",
	fx.Provide(
		NewRepository,
		newRuntimeQuerySource,
		NewService,
		AsReader,
		AsHandlerService,
		NewHandler,
	),
	fx.Provide(
		fx.Annotate(RoutesAdmin, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
