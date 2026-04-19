package system

import "go.uber.org/fx"

var Module = fx.Module("system",
	fx.Provide(
		NewRepository,
		fx.Annotate(
			func(repo *Repository) *Repository { return repo },
			fx.As(new(Reader)),
		),
		newRuntimeQuerySource,
		NewService,
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
