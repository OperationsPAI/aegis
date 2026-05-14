package auth

import (
	user "aegis/crud/iam/user"

	"go.uber.org/fx"
)

// asUserSessionRevoker binds *TokenStore as user.SessionRevoker so the user
// service can kick existing sessions on admin password resets.
func asUserSessionRevoker(ts *TokenStore) user.SessionRevoker { return ts }

var Module = fx.Module("auth",
	fx.Provide(NewUserRepository),
	fx.Provide(NewRoleRepository),
	fx.Provide(NewAPIKeyRepository),
	fx.Provide(NewTokenStore),
	fx.Provide(asUserSessionRevoker),
	fx.Provide(NewService),
	fx.Provide(AsHandlerService),
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
