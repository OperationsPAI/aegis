package systemmetric

import "go.uber.org/fx"

var Module = fx.Module("system_metric",
	fx.Provide(NewRepository),
	fx.Provide(NewService),
	fx.Provide(fx.Annotate(AsReader, fx.As(new(Reader)))),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(RoutesAdmin, fx.ResultTags(`group:"routes"`)),
	),
	fx.Invoke(RegisterMetricsCollector),
)
