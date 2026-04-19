package sdk

import "go.uber.org/fx"

var Module = fx.Module("sdk",
	fx.Provide(NewRepository),
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	fx.Provide(AsRoutesHandler),
	fx.Provide(
		fx.Annotate(RoutesSDK, fx.ResultTags(`group:"routes"`)),
	),
)
