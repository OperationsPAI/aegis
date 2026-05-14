package sso

import (
	"errors"
	"time"

	"aegis/crud/iam/user"
	"aegis/platform/config"
	"aegis/platform/jwtkeys"
	"aegis/platform/redis"

	"github.com/gin-gonic/gin"
)

const iamTracerName = "aegis/iam"

const (
	authReqRedisPrefix = "sso:authreq:"
	authReqTTL         = 10 * time.Minute
	refreshRedisPrefix = "sso:refresh:"
	refreshTokenTTL    = 7 * 24 * time.Hour
)

// authRequest persists between /authorize and /token. It is stored in
// Redis under sso:authreq:<code>, keyed by the auth code the OP returns to
// the relying-party.
type authRequest struct {
	ClientID            string `json:"client_id"`
	UserID              int    `json:"user_id"`
	RedirectURI         string `json:"redirect_uri"`
	State               string `json:"state,omitempty"`
	Scope               string `json:"scope,omitempty"`
	CodeChallenge       string `json:"code_challenge,omitempty"`
	CodeChallengeMethod string `json:"code_challenge_method,omitempty"`
}

type refreshRecord struct {
	UserID   int    `json:"user_id"`
	ClientID string `json:"client_id"`
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// OIDCService exposes the SSO endpoints described in §4 of
// sso-extraction-design.md. It does not depend on zitadel/oidc/v3 — the
// scope of the OP is intentionally narrow (auth code, refresh, client
// credentials, password grant for tests) so a focused implementation is
// smaller and easier to audit than the storage interfaces required by the
// upstream framework.
type OIDCService struct {
	signer      *jwtkeys.Signer
	clients     *Service
	users       *user.Service
	redis       *redis.Gateway
	issuer      string
	jwksHandler *jwksDoc
}

func NewOIDCService(signer *jwtkeys.Signer, clients *Service, users *user.Service, redisGW *redis.Gateway) (*OIDCService, error) {
	doc, err := newJWKSDoc(signer)
	if err != nil {
		return nil, err
	}
	issuer := config.GetString("sso.issuer")
	if issuer == "" {
		if config.IsProduction() {
			return nil, errors.New("sso.issuer is required in production")
		}
		issuer = "http://localhost:8083"
	}
	return &OIDCService{
		signer:      signer,
		clients:     clients,
		users:       users,
		redis:       redisGW,
		issuer:      issuer,
		jwksHandler: doc,
	}, nil
}

// RegisterOIDCRoutes mounts discovery, JWKS, authorize, token, userinfo,
// and logout on the engine root.
func RegisterOIDCRoutes(engine *gin.Engine, svc *OIDCService) {
	engine.GET("/.well-known/openid-configuration", svc.discovery)
	engine.GET("/.well-known/jwks.json", svc.jwks)
	engine.GET("/authorize", svc.authorizeGet)
	engine.POST("/login", svc.loginPost)
	engine.POST("/token", svc.token)
	engine.GET("/userinfo", svc.userinfo)
	engine.POST("/v1/logout", svc.logout)
}
