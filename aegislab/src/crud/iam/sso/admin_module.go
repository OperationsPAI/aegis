package sso

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"aegis/crud/iam/rbac"
	"aegis/crud/iam/user"
	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/jwtkeys"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

// AdminModule wires the /v1/* admin REST surface (permission checks, user
// lookups, grants, service accounts), the JWKS endpoint, and a minimal
// /token endpoint (client_credentials only) into aegis-api so the
// standalone aegis-sso binary can be decommissioned.
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
	registerTokenEndpoint(engine, signer)
}

func registerJWKSEndpoint(engine *gin.Engine, signer *jwtkeys.Signer) {
	doc := jwtkeys.JWKSFromPublicKey(signer.PublicKey(), signer.Kid)
	body, _ := json.Marshal(doc)
	engine.GET("/.well-known/jwks.json", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json", body)
	})
}

func registerTokenEndpoint(engine *gin.Engine, signer *jwtkeys.Signer) {
	clientID := config.GetString("sso.client_id")
	clientSecret := config.GetString("sso.client_secret")
	if clientSecret == "" {
		if f := config.GetString("sso.client_secret_file"); f != "" {
			if b, err := os.ReadFile(f); err == nil {
				clientSecret = string(b)
			}
		}
	}

	engine.POST("/token", func(c *gin.Context) {
		grant := c.PostForm("grant_type")
		if grant != consts.OIDCGrantClientCredentials {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":             "unsupported_grant_type",
				"error_description": "only client_credentials is supported",
			})
			return
		}

		id, secret, hasBasic := c.Request.BasicAuth()
		if !hasBasic {
			id = c.PostForm("client_id")
			secret = c.PostForm("client_secret")
		}
		if id != clientID || secret != clientSecret {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":             "invalid_client",
				"error_description": "client authentication failed",
			})
			return
		}

		signed, exp, err := crypto.GenerateUnifiedToken(crypto.UnifiedTokenParams{
			Typ:      "service_account",
			Service:  id,
			Scopes:   []string{"openid", "profile", "email"},
			AuthType: consts.AuthTypeServiceAccount,
			Audience: []string{consts.AudienceSSOInternal},
			Lifetime: crypto.ServiceTokenExpiration,
		}, signer.PrivateKey, signer.Kid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"access_token": signed,
			"token_type":   consts.TokenTypeBearer,
			"expires_in":   int64(time.Until(exp).Seconds()),
		})
	})
}

