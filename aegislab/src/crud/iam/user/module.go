package user

import "go.uber.org/fx"

// CoreModule provides only the data-layer components (Repository + Service)
// without the HTTP handler, routes, or permissions. Use this in binaries that
// need user lookups but do not serve the user admin HTTP surface.
var CoreModule = fx.Module("user.core",
	fx.Provide(NewRepository),
	fx.Provide(NewService),
)

var Module = fx.Module("user",
	CoreModule,
	fx.Provide(fx.Annotate(AsReader, fx.As(new(Reader)))),
	fx.Provide(AsHandlerService),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(RoutesAdmin, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
