package dataset

import (
	"aegis/framework"

	"go.uber.org/fx"
)

var Module = fx.Module("dataset",
	fx.Provide(NewRepository),
	fx.Provide(fx.Annotate(AsReader, fx.As(new(Reader)))),
	fx.Provide(NewDatapackFileStore),
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
	fx.Provide(
		NewHandler,
		fx.Annotate(NewHandler, fx.As(new(framework.DatasetHandler))),
	),
	fx.Provide(
		fx.Annotate(RoutesPortal, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(RoutesSDK, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
