package team

import (
	"aegis/framework"

	"go.uber.org/fx"
)

var Module = fx.Module("team",
	fx.Provide(NewRepository),
	fx.Provide(newProjectReader),
	fx.Provide(NewService),
	fx.Provide(fx.Annotate(AsReader, fx.As(new(Reader)))),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	fx.Provide(fx.Annotate(AsRoutesHandler, fx.As(new(framework.TeamRoutesHandler)))),
	fx.Provide(
		fx.Annotate(RoutesPortal, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
