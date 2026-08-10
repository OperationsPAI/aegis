package cluster

import "go.uber.org/fx"

var Module = fx.Module("cluster",
	fx.Provide(
		NewLiveEnvFromDisk,
		NewLiveCheckRunner,
		NewService,
		AsHandlerService,
		NewHandler,
	),
	fx.Provide(
		fx.Annotate(RoutesPortal, fx.ResultTags(`group:"routes"`)),
	),
)
