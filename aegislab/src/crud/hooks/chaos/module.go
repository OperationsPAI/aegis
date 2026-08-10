package chaoshooks

import "go.uber.org/fx"

var Module = fx.Module("hooks.chaos",
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(Routes, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
