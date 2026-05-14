package sso

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/model"
	"aegis/platform/tracing"
	"aegis/platform/utils"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
)

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
	if err != nil || !crypto.VerifyPassword(password, u.Password) || !u.IsActive {
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
