package sso

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/jwtkeys"
	"aegis/platform/redis"
	"aegis/platform/model"
	"aegis/crud/iam/user"
	"aegis/platform/tracing"
	"aegis/platform/utils"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel"
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

type jwksDoc struct {
	cached jwtkeys.JWKS
	json   []byte
}

func newJWKSDoc(pub *jwtkeys.Signer) (*jwksDoc, error) {
	jwks := jwtkeys.JWKSFromPublicKey(pub.PublicKey(), pub.Kid)
	body, err := json.Marshal(jwks)
	if err != nil {
		return nil, err
	}
	return &jwksDoc{cached: jwks, json: body}, nil
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

func (s *OIDCService) discovery(c *gin.Context) {
	iss := s.issuer
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                iss,
		"authorization_endpoint":                iss + "/authorize",
		"token_endpoint":                        iss + "/token",
		"userinfo_endpoint":                     iss + "/userinfo",
		"jwks_uri":                              iss + "/.well-known/jwks.json",
		"end_session_endpoint":                  iss + "/v1/logout",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 consts.OIDCGrantsSupported,
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      consts.OIDCScopesSupported,
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
	})
}

func (s *OIDCService) jwks(c *gin.Context) {
	_, span := otel.Tracer(iamTracerName).Start(c.Request.Context(), "iam/sso/jwks")
	defer span.End()
	c.Data(http.StatusOK, "application/json", s.jwksHandler.json)
}

// authorizeGet renders a minimal login form. Real deployments swap this
// for a frontend SPA; PR-1b ships an inline form so e2e tests can drive
// the auth_code flow without depending on AegisLab-frontend.
func (s *OIDCService) authorizeGet(c *gin.Context) {
	ctx, span := otel.Tracer(iamTracerName).Start(c.Request.Context(), "iam/sso/authorize")
	defer span.End()
	clientID := c.Query("client_id")
	tracing.SetSpanAttribute(ctx, "sso.client_id", clientID)
	redirectURI := c.Query("redirect_uri")
	state := c.Query("state")
	scope := c.Query("scope")
	respType := c.Query("response_type")
	codeChallenge := c.Query("code_challenge")
	codeChallengeMethod := c.Query("code_challenge_method")

	if respType != "code" {
		c.String(http.StatusBadRequest, "unsupported response_type")
		return
	}
	cli, err := s.clients.GetByClientID(c.Request.Context(), clientID)
	if err != nil {
		c.String(http.StatusBadRequest, "unknown client_id")
		return
	}
	if !grantAllowed(cli, consts.OIDCGrantAuthorizationCode) {
		c.String(http.StatusBadRequest, "client not configured for authorization_code")
		return
	}
	if !redirectAllowed(cli, redirectURI) {
		c.String(http.StatusBadRequest, "redirect_uri not registered")
		return
	}
	if !cli.IsConfidential && codeChallenge == "" {
		c.String(http.StatusBadRequest, "public client requires code_challenge (PKCE)")
		return
	}
	if codeChallenge != "" {
		if codeChallengeMethod == "" {
			codeChallengeMethod = consts.PKCEMethodPlain
		}
		if codeChallengeMethod != consts.PKCEMethodS256 && codeChallengeMethod != consts.PKCEMethodPlain {
			c.String(http.StatusBadRequest, "unsupported code_challenge_method")
			return
		}
	}

	// When a `[sso] login_redirect` is configured, hand off rendering of the
	// login UI to the console — re-emit every OIDC param (including PKCE)
	// so the console can POST them back to /login unchanged.
	if dest := config.GetString("sso.login_redirect"); dest != "" {
		u, err := url.Parse(dest)
		if err == nil {
			q := u.Query()
			q.Set("client_id", clientID)
			q.Set("redirect_uri", redirectURI)
			q.Set("state", state)
			q.Set("scope", scope)
			q.Set("response_type", respType)
			if codeChallenge != "" {
				q.Set("code_challenge", codeChallenge)
				q.Set("code_challenge_method", codeChallengeMethod)
			}
			u.RawQuery = q.Encode()
			c.Redirect(http.StatusFound, u.String())
			return
		}
	}

	html := fmt.Sprintf(`<!doctype html><html><body>
<h2>Sign in to %s</h2>
<form method="POST" action="/login">
<input type="hidden" name="client_id" value="%s"/>
<input type="hidden" name="redirect_uri" value="%s"/>
<input type="hidden" name="state" value="%s"/>
<input type="hidden" name="scope" value="%s"/>
<input type="hidden" name="code_challenge" value="%s"/>
<input type="hidden" name="code_challenge_method" value="%s"/>
<label>Username <input name="username"/></label><br/>
<label>Password <input name="password" type="password"/></label><br/>
<button type="submit">Sign in</button>
</form></body></html>`,
		htmlEscape(cli.Name), htmlEscape(clientID), htmlEscape(redirectURI),
		htmlEscape(state), htmlEscape(scope),
		htmlEscape(codeChallenge), htmlEscape(codeChallengeMethod))
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

func (s *OIDCService) loginPost(c *gin.Context) {
	ctx, span := otel.Tracer(iamTracerName).Start(c.Request.Context(), "iam/sso/login")
	defer span.End()
	clientID := c.PostForm("client_id")
	tracing.SetSpanAttribute(ctx, "sso.client_id", clientID)
	redirectURI := c.PostForm("redirect_uri")
	state := c.PostForm("state")
	scope := c.PostForm("scope")
	username := c.PostForm("username")
	password := c.PostForm("password")
	codeChallenge := c.PostForm("code_challenge")
	codeChallengeMethod := c.PostForm("code_challenge_method")

	cli, err := s.clients.GetByClientID(c.Request.Context(), clientID)
	if err != nil || !grantAllowed(cli, consts.OIDCGrantAuthorizationCode) || !redirectAllowed(cli, redirectURI) {
		c.String(http.StatusBadRequest, "invalid client or redirect_uri")
		return
	}
	if !cli.IsConfidential && codeChallenge == "" {
		c.String(http.StatusBadRequest, "public client requires PKCE")
		return
	}
	u, err := s.users.GetByUsername(c.Request.Context(), username)
	if err != nil && strings.Contains(username, "@") {
		u, err = s.users.GetByEmail(c.Request.Context(), username)
	}
	if err != nil || !utils.VerifyPassword(password, u.Password) || !u.IsActive {
		c.String(http.StatusUnauthorized, "invalid credentials")
		return
	}
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(u.ID))

	code, err := randomToken(24)
	if err != nil {
		c.String(http.StatusInternalServerError, "code generation failed")
		return
	}
	if codeChallenge != "" && codeChallengeMethod == "" {
		codeChallengeMethod = consts.PKCEMethodPlain
	}
	ar := authRequest{
		ClientID:            cli.ClientID,
		UserID:              u.ID,
		RedirectURI:         redirectURI,
		State:               state,
		Scope:               scope,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
	}
	if err := s.storeAuthRequest(c.Request.Context(), code, ar); err != nil {
		c.String(http.StatusInternalServerError, "auth state persistence failed")
		return
	}
	dest, err := buildRedirect(redirectURI, code, state)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid redirect_uri")
		return
	}
	c.Redirect(http.StatusFound, dest)
}

func buildRedirect(redirectURI, code, state string) (string, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *OIDCService) storeAuthRequest(ctx context.Context, code string, ar authRequest) error {
	body, err := json.Marshal(ar)
	if err != nil {
		return err
	}
	return s.redis.Set(ctx, authReqRedisPrefix+code, string(body), authReqTTL)
}

func (s *OIDCService) consumeAuthRequest(ctx context.Context, code string) (*authRequest, error) {
	key := authReqRedisPrefix + code
	raw, err := s.redis.GetString(ctx, key)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, errors.New("auth code unknown or expired")
	}
	var ar authRequest
	if err := json.Unmarshal([]byte(raw), &ar); err != nil {
		return nil, err
	}
	_, _ = s.redis.DeleteKey(ctx, key)
	return &ar, nil
}

func (s *OIDCService) storeRefresh(ctx context.Context, rt string, rec refreshRecord) error {
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.redis.Set(ctx, refreshRedisPrefix+rt, string(body), refreshTokenTTL)
}

func (s *OIDCService) loadRefresh(ctx context.Context, rt string) (*refreshRecord, error) {
	raw, err := s.redis.GetString(ctx, refreshRedisPrefix+rt)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, errors.New("refresh token unknown or expired")
	}
	var rec refreshRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

func (s *OIDCService) token(c *gin.Context) {
	ctx, span := otel.Tracer(iamTracerName).Start(c.Request.Context(), "iam/sso/token")
	defer span.End()
	grant := c.PostForm("grant_type")
	tracing.SetSpanAttribute(ctx, "sso.grant_type", grant)
	cli, ok := s.authenticateClient(c)
	if !ok {
		return
	}
	if !grantAllowed(cli, grant) {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorUnauthorizedClient, "grant not allowed for client")
		return
	}

	switch grant {
	case consts.OIDCGrantAuthorizationCode:
		s.grantAuthCode(c, cli)
	case consts.OIDCGrantRefreshToken:
		s.grantRefresh(c, cli)
	case consts.OIDCGrantClientCredentials:
		s.grantClientCredentials(c, cli)
	case consts.OIDCGrantPassword:
		s.grantPassword(c, cli)
	default:
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorUnsupportedGrantType, "unsupported grant_type")
	}
}

func tokenError(c *gin.Context, status int, code, desc string) {
	c.JSON(status, gin.H{"error": code, "error_description": desc})
}

// authenticateClient resolves the client via Basic or POST body credentials.
// Returns the client on success and writes a 401 + nil on failure.
func (s *OIDCService) authenticateClient(c *gin.Context) (*model.OIDCClient, bool) {
	clientID, clientSecret, hasBasic := c.Request.BasicAuth()
	if !hasBasic {
		clientID = c.PostForm("client_id")
		clientSecret = c.PostForm("client_secret")
	}
	if clientID == "" {
		tokenError(c, http.StatusUnauthorized, consts.OIDCErrorInvalidClient, "missing client_id")
		return nil, false
	}
	cli, err := s.clients.VerifySecret(c.Request.Context(), clientID, clientSecret)
	if err != nil {
		tokenError(c, http.StatusUnauthorized, consts.OIDCErrorInvalidClient, "client authentication failed")
		return nil, false
	}
	return cli, true
}

func (s *OIDCService) grantAuthCode(c *gin.Context, cli *model.OIDCClient) {
	code := c.PostForm("code")
	redirectURI := c.PostForm("redirect_uri")
	ar, err := s.consumeAuthRequest(c.Request.Context(), code)
	if err != nil {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, err.Error())
		return
	}
	if ar.ClientID != cli.ClientID || ar.RedirectURI != redirectURI {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "code does not match client/redirect")
		return
	}
	codeVerifier := c.PostForm("code_verifier")
	if ar.CodeChallenge != "" {
		if codeVerifier == "" {
			tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "code_verifier required")
			return
		}
		if !verifyPKCE(ar.CodeChallenge, ar.CodeChallengeMethod, codeVerifier) {
			tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "code_verifier mismatch")
			return
		}
	} else if !cli.IsConfidential {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "public client requires PKCE")
		return
	}
	u, err := s.users.GetByID(c.Request.Context(), ar.UserID)
	if err != nil {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "user gone")
		return
	}
	s.respondUserToken(c, cli, u, true)
}

func (s *OIDCService) grantRefresh(c *gin.Context, cli *model.OIDCClient) {
	rt := c.PostForm("refresh_token")
	rec, err := s.loadRefresh(c.Request.Context(), rt)
	if err != nil {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, err.Error())
		return
	}
	if rec.ClientID != cli.ClientID {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "refresh token bound to different client")
		return
	}
	u, err := s.users.GetByID(c.Request.Context(), rec.UserID)
	if err != nil {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "user gone")
		return
	}
	// Rotate the refresh token to invalidate the replayed one.
	_, _ = s.redis.DeleteKey(c.Request.Context(), refreshRedisPrefix+rt)
	s.respondUserToken(c, cli, u, true)
}

func (s *OIDCService) grantClientCredentials(c *gin.Context, cli *model.OIDCClient) {
	exp := time.Now().Add(utils.ServiceTokenExpiration)
	claims := jwt.MapClaims{
		"iss":                     s.issuer,
		consts.OIDCClaimSubject:   consts.ClaimSubjectServicePrefix + cli.Service,
		consts.OIDCClaimAudience:  []string{consts.AudienceSSOInternal},
		"exp":                     exp.Unix(),
		"iat":                     time.Now().Unix(),
		"service":                 cli.Service,
		"scopes":                  cli.Scopes,
		consts.OIDCClaimTokenType: consts.TokenTypeService,
	}
	signed, err := signWithKid(claims, s.signer)
	if err != nil {
		tokenError(c, http.StatusInternalServerError, consts.OIDCErrorServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, tokenResp{
		AccessToken: signed,
		TokenType:   consts.TokenTypeBearer,
		ExpiresIn:   int64(time.Until(exp).Seconds()),
	})
}

func (s *OIDCService) grantPassword(c *gin.Context, cli *model.OIDCClient) {
	username := c.PostForm("username")
	password := c.PostForm("password")
	u, err := s.users.GetByUsername(c.Request.Context(), username)
	if err != nil && strings.Contains(username, "@") {
		u, err = s.users.GetByEmail(c.Request.Context(), username)
	}
	if err != nil || !utils.VerifyPassword(password, u.Password) || !u.IsActive {
		tokenError(c, http.StatusUnauthorized, consts.OIDCErrorInvalidGrant, "invalid credentials")
		return
	}
	s.respondUserToken(c, cli, u, false)
}

func (s *OIDCService) respondUserToken(c *gin.Context, cli *model.OIDCClient, u *model.User, withRefresh bool) {
	roles, _ := s.users.ListRoleNames(c.Request.Context(), u.ID)
	isAdmin := false
	for _, r := range roles {
		if r == string(consts.RoleSuperAdmin) || r == string(consts.RoleAdmin) {
			isAdmin = true
			break
		}
	}
	access, expiresAt, err := utils.GenerateToken(u.ID, u.Username, u.Email, u.IsActive, isAdmin, roles, s.signer.PrivateKey, s.signer.Kid)
	if err != nil {
		tokenError(c, http.StatusInternalServerError, consts.OIDCErrorServerError, err.Error())
		return
	}
	resp := tokenResp{
		AccessToken: access,
		TokenType:   consts.TokenTypeBearer,
		ExpiresIn:   int64(time.Until(expiresAt).Seconds()),
	}
	if withRefresh && grantAllowed(cli, consts.OIDCGrantRefreshToken) {
		rt, err := randomToken(32)
		if err != nil {
			tokenError(c, http.StatusInternalServerError, consts.OIDCErrorServerError, err.Error())
			return
		}
		if err := s.storeRefresh(c.Request.Context(), rt, refreshRecord{UserID: u.ID, ClientID: cli.ClientID}); err != nil {
			tokenError(c, http.StatusInternalServerError, consts.OIDCErrorServerError, err.Error())
			return
		}
		resp.RefreshToken = rt
	}
	c.JSON(http.StatusOK, resp)
}

func (s *OIDCService) userinfo(c *gin.Context) {
	_, span := otel.Tracer(iamTracerName).Start(c.Request.Context(), "iam/sso/userinfo")
	defer span.End()
	token, err := utils.ExtractTokenFromHeader(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return
	}
	claims, err := utils.ParseToken(token, func(kid string) (*rsa.PublicKey, error) {
		if kid != "" && kid != s.signer.Kid {
			return nil, errors.New("unknown kid")
		}
		return s.signer.PublicKey(), nil
	})
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return
	}
	u, err := s.users.GetByID(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user_not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		consts.OIDCClaimSubject:           strconv.Itoa(u.ID),
		consts.OIDCClaimPreferredUsername: u.Username,
		consts.OIDCClaimEmail:             u.Email,
		consts.OIDCClaimEmailVerified:     true,
		consts.OIDCClaimName:              u.FullName,
		consts.OIDCClaimPicture:           u.Avatar,
	})
}

func (s *OIDCService) logout(c *gin.Context) {
	_, span := otel.Tracer(iamTracerName).Start(c.Request.Context(), "iam/sso/logout")
	defer span.End()
	rt := c.PostForm("refresh_token")
	if rt != "" {
		_, _ = s.redis.DeleteKey(c.Request.Context(), refreshRedisPrefix+rt)
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func grantAllowed(cli *model.OIDCClient, grant string) bool {
	for _, g := range cli.Grants {
		if g == grant {
			return true
		}
	}
	return false
}

func redirectAllowed(cli *model.OIDCClient, uri string) bool {
	for _, r := range cli.RedirectURIs {
		if r == uri {
			return true
		}
	}
	return false
}

// verifyPKCE checks that a code_verifier matches the stored challenge per
// RFC 7636. `plain` compares directly; `S256` compares base64url(sha256(v)).
func verifyPKCE(challenge, method, verifier string) bool {
	switch method {
	case "", consts.PKCEMethodPlain:
		return challenge == verifier
	case consts.PKCEMethodS256:
		sum := sha256.Sum256([]byte(verifier))
		return challenge == base64.RawURLEncoding.EncodeToString(sum[:])
	default:
		return false
	}
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func signWithKid(claims jwt.Claims, signer *jwtkeys.Signer) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if signer.Kid != "" {
		tok.Header["kid"] = signer.Kid
	}
	return tok.SignedString(signer.PrivateKey)
}
