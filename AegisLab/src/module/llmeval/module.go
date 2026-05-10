package llmeval

import "go.uber.org/fx"

var Module = fx.Module("llmeval",
	fx.Provide(
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
