package user

import "go.uber.org/fx"

var Module = fx.Module("user",
	fx.Provide(NewRepository),
	fx.Provide(NewService),
	fx.Provide(fx.Annotate(AsReader, fx.As(new(Reader)))),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(RoutesAdmin, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
