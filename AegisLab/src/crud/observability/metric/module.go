package metric

import "go.uber.org/fx"

var Module = fx.Module("metric",
	fx.Provide(NewRepository),
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	// Metric is a read-only query module: it contributes SDK routes, but
	// it does not own RBAC rules, role grants, or database tables.
	fx.Provide(
		fx.Annotate(RoutesSDK, fx.ResultTags(`group:"routes"`)),
	),
)
