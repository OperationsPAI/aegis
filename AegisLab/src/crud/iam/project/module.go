package project

import (
	"go.uber.org/fx"
)

var Module = fx.Module("project",
	fx.Provide(
		NewRepository,
		newProjectStatisticsSource,
		NewService,
		fx.Annotate(AsReader, fx.As(new(Reader))),
		AsHandlerService,
		NewHandler,
	),
	fx.Provide(
		fx.Annotate(RoutesPortal, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
