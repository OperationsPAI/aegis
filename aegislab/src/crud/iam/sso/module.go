package sso

import (
	"aegis/crud/iam/rbac"
	"aegis/platform/auth"
	"aegis/platform/redis"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

func newRevocationStore(gw *redis.Gateway) auth.RevocationStore {
	return auth.NewRedisRevocationStore(gw.Client())
}

// Module wires the SSO admin REST surface (`/v1/*` per
// sso-extraction-design.md §5), the OIDC client management API, and the
// OIDC OP endpoints (discovery / jwks / authorize / token / userinfo /
// logout). Loaded only by `app/sso` so the aegislab backend binary never
// registers these endpoints.
var Module = fx.Module("sso",
	fx.Provide(
		NewRepository,
		NewService,
		NewHandler,
		NewAdminService,
		NewAdminHandler,
		NewOIDCService,
		NewServiceAccountRepository,
		NewServiceAccountService,
		NewServiceAccountHandler,
		NewFederationRepository,
		NewFederationHandler,
		newRevocationStore,
	),
	fx.Provide(
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
	fx.Invoke(func(engine *gin.Engine, admin *AdminHandler, client *Handler, oidc *OIDCService, sa *ServiceAccountHandler, fed *FederationHandler, rbacRepo *rbac.Repository) {
		SetAdminScopeResolver(rbacRepo)
		RegisterAdminRoutes(engine, admin)
		RegisterClientRoutes(engine, client)
		RegisterOIDCRoutes(engine, oidc)
		RegisterServiceAccountRoutes(engine, sa)
		RegisterFederationRoutes(engine, fed)
	}),
)
