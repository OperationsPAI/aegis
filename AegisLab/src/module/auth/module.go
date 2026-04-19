package auth

import (
	"go.uber.org/fx"
)

var Module = fx.Module("auth",
	fx.Provide(NewUserRepository),
	fx.Provide(NewRoleRepository),
	fx.Provide(NewAPIKeyRepository),
	fx.Provide(NewTokenStore),
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
	fx.Provide(NewTokenVerifier),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(RoutesPublic, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(RoutesSDK, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(RoutesPortal, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Permissions, fx.ResultTags(`group:"permissions"`)),
		fx.Annotate(RoleGrants, fx.ResultTags(`group:"role_grants"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)
