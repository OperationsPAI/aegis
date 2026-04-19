package systemmetric

import "go.uber.org/fx"

var Module = fx.Module("system_metric",
	fx.Provide(NewRepository),
	fx.Provide(NewService),
	fx.Provide(fx.Annotate(AsReader, fx.As(new(Reader)))),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	// systemmetric contributes admin routes. It also keeps explicit
	// no-op permission / role-grant / migration registrars so the Phase 4
	// module layout matches the reference template without claiming
	// ownership of system-module permissions or SQL entities.
	fx.Provide(
		fx.Annotate(RoutesAdmin, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
	fx.Invoke(RegisterMetricsCollector),
)
