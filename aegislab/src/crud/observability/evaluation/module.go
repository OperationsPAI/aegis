package evaluation

import "go.uber.org/fx"

var Module = fx.Module("evaluation",
	fx.Provide(NewRepository),
	fx.Provide(newExecutionQuerySource),
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(RoutesPortal, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(RoutesSDK, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
