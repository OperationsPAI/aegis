package chaossystem

import "go.uber.org/fx"

var Module = fx.Module("chaossystem",
	fx.Provide(NewRepository),
	fx.Provide(NewService),
	fx.Provide(fx.Annotate(AsReader, fx.As(new(Reader)))),
	fx.Provide(fx.Annotate(AsWriter, fx.As(new(Writer)))),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(RoutesAdmin, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
