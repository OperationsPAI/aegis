package pages

import "go.uber.org/fx"

// Module wires the pages module: repository → service → handlers → routes.
// SSR + management share the same Service; the two handlers are split so
// auth wiring stays explicit at the route level.
var Module = fx.Module("pages",
	fx.Provide(NewRepository),
	fx.Provide(NewService),
	fx.Provide(NewHandler),
	fx.Provide(NewRenderHandler),

	fx.Provide(
		fx.Annotate(RoutesPortal, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(RoutesSDK, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(RoutesEngine, fx.ResultTags(`group:"engine_routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
