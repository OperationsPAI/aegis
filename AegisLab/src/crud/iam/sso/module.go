package sso

import (
	"aegis/crud/iam/rbac"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

// Module wires the SSO admin REST surface (`/v1/*` per
// sso-extraction-design.md §5), the OIDC client management API, and the
// OIDC OP endpoints (discovery / jwks / authorize / token / userinfo /
// logout). Loaded only by `app/sso` so the AegisLab backend binary never
// registers these endpoints.
var Module = fx.Module("sso",
	fx.Provide(
		NewRepository,
		NewService,
		NewHandler,
		NewAdminService,
		NewAdminHandler,
		NewOIDCService,
	),
	fx.Provide(
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
	fx.Invoke(func(engine *gin.Engine, admin *AdminHandler, client *Handler, oidc *OIDCService, rbacRepo *rbac.Repository) {
		// Register the rbac repository as the scope resolver consulted by
		// requireAdminOrService for delegated service admins (Task #13).
		SetAdminScopeResolver(rbacRepo)
		RegisterAdminRoutes(engine, admin)
		RegisterClientRoutes(engine, client)
		RegisterOIDCRoutes(engine, oidc)
	}),
)
