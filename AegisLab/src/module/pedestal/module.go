package pedestal

import "go.uber.org/fx"

var Module = fx.Module("pedestal",
	fx.Provide(NewRepository),
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
	fx.Provide(AsReader),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(Routes, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
