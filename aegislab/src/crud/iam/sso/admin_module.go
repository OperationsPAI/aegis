package sso

import (
	"encoding/json"
	"net/http"

	"aegis/crud/iam/rbac"
	"aegis/crud/iam/user"
	"aegis/platform/jwtkeys"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

// AdminModule wires the /v1/* admin REST surface (permission checks, user
// lookups, grants, service accounts) and the JWKS endpoint into aegis-api
// so the standalone aegis-sso binary can be decommissioned. OIDC provider
// endpoints are NOT included — Casdoor replaces those.
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

func registerAdminSurface(engine *gin.Engine, admin *AdminHandler, sa *ServiceAccountHandler, rbacRepo *rbac.Repository, signer *jwtkeys.Signer) {
	SetAdminScopeResolver(rbacRepo)
	RegisterAdminRoutes(engine, admin)
	RegisterServiceAccountRoutes(engine, sa)
	registerJWKSEndpoint(engine, signer)
}

func registerJWKSEndpoint(engine *gin.Engine, signer *jwtkeys.Signer) {
	doc := jwtkeys.JWKSFromPublicKey(signer.PublicKey(), signer.Kid)
	body, _ := json.Marshal(doc)
	engine.GET("/.well-known/jwks.json", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json", body)
	})
}
