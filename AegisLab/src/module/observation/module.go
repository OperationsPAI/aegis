package observation

import "go.uber.org/fx"

var Module = fx.Module("observation",
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(RoutesPortal, fx.ResultTags(`group:"routes"`)),
	),
)
