package sso

import (
	"aegis/crud/iam/rbac"
	"aegis/crud/iam/user"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

// AdminModule wires the /v1/* admin REST surface (permission checks, user
// lookups, grants, service accounts) into the aegis-api process so the
// standalone aegis-sso binary can be decommissioned. OIDC provider
// endpoints are NOT included — Authentik replaces those.
var AdminModule = fx.Module("iam-admin",
	fx.Provide(user.NewRepository),
	fx.Provide(user.NewService),
	fx.Provide(NewAdminService),
	fx.Provide(NewAdminHandler),
	fx.Provide(NewServiceAccountRepository),
	fx.Provide(NewServiceAccountService),
	fx.Provide(NewServiceAccountHandler),
	fx.Invoke(registerAdminSurface),
)

func registerAdminSurface(engine *gin.Engine, admin *AdminHandler, sa *ServiceAccountHandler, rbacRepo *rbac.Repository) {
	SetAdminScopeResolver(rbacRepo)
	RegisterAdminRoutes(engine, admin)
	RegisterServiceAccountRoutes(engine, sa)
}
