package dashboard

import "go.uber.org/fx"

var Module = fx.Module("dashboard",
	fx.Provide(
		NewService,
		AsHandlerService,
		NewHandler,
	),
	fx.Provide(
		fx.Annotate(RoutesPortal, fx.ResultTags(`group:"routes"`)),
	),
)
