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

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
)

// discovery serves the OIDC discovery document.
//
//	@Summary		OIDC discovery document
//	@Description	OIDC 1.0 discovery metadata for the SSO issuer (endpoints, supported grants, scopes, signing algs).
//	@Tags			OIDC
//	@ID				oidc_discovery
//	@Produce		json
//	@Success		200	{object}	map[string]any	"Discovery document"
//	@Router			/.well-known/openid-configuration [get]
func (s *OIDCService) discovery(c *gin.Context) {
	iss := s.issuer
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                iss,
		"authorization_endpoint":                iss + "/authorize",
		"token_endpoint":                        iss + "/token",
		"userinfo_endpoint":                     iss + "/userinfo",
		"jwks_uri":                              iss + "/.well-known/jwks.json",
		"end_session_endpoint":                  iss + "/v1/logout",
		"response_types_supported":              []string{consts.OIDCResponseTypeCode},
		"grant_types_supported":                 consts.OIDCGrantsSupported,
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      consts.OIDCScopesSupported,
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
	})
}

// jwks serves the JSON Web Key Set used to verify SSO-issued tokens.
//
//	@Summary		JWKS
//	@Description	Public key set (JWKS) for verifying SSO-issued JWTs.
//	@Tags			OIDC
//	@ID				oidc_jwks
//	@Produce		json
//	@Success		200	{object}	map[string]any	"JWKS document"
//	@Router			/.well-known/jwks.json [get]
func (s *OIDCService) jwks(c *gin.Context) {
	_, span := otel.Tracer(iamTracerName).Start(c.Request.Context(), "iam/sso/jwks")
	defer span.End()
	c.Data(http.StatusOK, "application/json", s.jwksHandler.json)
}

// authorizeGet renders a minimal login form. Real deployments swap this
// for a frontend SPA; PR-1b ships an inline form so e2e tests can drive
// the auth_code flow without depending on AegisLab-frontend.
// authorizeGet renders the OIDC authorization endpoint.
//
//	@Summary		OIDC authorize endpoint
//	@Description	Standard OIDC authorize endpoint. Validates `client_id`, `redirect_uri`, `response_type=code`, and (for public clients) PKCE; either renders the inline login form or 302-redirects to the configured `sso.login_redirect` console URL with the OIDC params preserved.
//	@Tags			OIDC
//	@ID				oidc_authorize
//	@Produce		html
//	@Param			client_id				query		string	true	"OIDC client_id"
//	@Param			redirect_uri			query		string	true	"Registered redirect URI"
//	@Param			response_type			query		string	true	"Must be `code`"
//	@Param			state					query		string	false	"Opaque state echoed back to redirect_uri"
//	@Param			scope					query		string	false	"Requested scope"
//	@Param			code_challenge			query		string	false	"PKCE code challenge (required for public clients)"
//	@Param			code_challenge_method	query		string	false	"PKCE method (`S256` or `plain`)"
//	@Success		200	{string}	string	"Inline login HTML"
//	@Success		302	{string}	string	"Redirect to configured login UI"
//	@Failure		400	{string}	string	"Invalid request"
//	@Router			/authorize [get]
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

	if respType != consts.OIDCResponseTypeCode {
		s.redirectLoginError(c, consts.LoginErrorUnsupportedResponseType, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
		return
	}
	cli, err := s.clients.GetByClientID(c.Request.Context(), clientID)
	if err != nil {
		s.redirectLoginError(c, consts.LoginErrorUnknownClient, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
		return
	}
	if !grantAllowed(cli, consts.OIDCGrantAuthorizationCode) {
		s.redirectLoginError(c, consts.LoginErrorClientNotConfigured, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
		return
	}
	if !redirectAllowed(cli, redirectURI) {
		s.redirectLoginError(c, consts.LoginErrorInvalidRedirectURI, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
		return
	}
	if !cli.IsConfidential && codeChallenge == "" {
		s.redirectLoginError(c, consts.LoginErrorPKCERequired, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
		return
	}
	if codeChallenge != "" {
		if codeChallengeMethod == "" {
			codeChallengeMethod = consts.PKCEMethodPlain
		}
		if codeChallengeMethod != consts.PKCEMethodS256 && codeChallengeMethod != consts.PKCEMethodPlain {
			s.redirectLoginError(c, consts.LoginErrorUnsupportedPKCEMethod, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
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

// loginPost completes the inline authorize flow with username/password.
//
//	@Summary		SSO inline login
//	@Description	Authenticate the resource owner from the inline authorize form (or the console hand-off), persist the auth request, and 302-redirect back to the relying party with `code` and `state`.
//	@Tags			OIDC
//	@ID				oidc_inline_login
//	@Accept			x-www-form-urlencoded
//	@Param			client_id				formData	string	true	"OIDC client_id"
//	@Param			redirect_uri			formData	string	true	"Registered redirect URI"
//	@Param			state					formData	string	false	"Opaque state echoed back"
//	@Param			scope					formData	string	false	"Requested scope"
//	@Param			username				formData	string	true	"Username or email"
//	@Param			password				formData	string	true	"Password"
//	@Param			code_challenge			formData	string	false	"PKCE code challenge"
//	@Param			code_challenge_method	formData	string	false	"PKCE method"
//	@Success		302	{string}	string	"Redirect to relying party with code"
//	@Failure		400	{string}	string	"Invalid client or redirect"
//	@Failure		401	{string}	string	"Invalid credentials"
//	@Failure		500	{string}	string	"Internal server error"
//	@Router			/login [post]
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
		s.redirectLoginError(c, consts.LoginErrorInvalidClientOrRedirect, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
		return
	}
	if !cli.IsConfidential && codeChallenge == "" {
		s.redirectLoginError(c, consts.LoginErrorPKCERequired, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
		return
	}
	u, err := s.users.GetByUsername(c.Request.Context(), username)
	if err != nil && strings.Contains(username, "@") {
		u, err = s.users.GetByEmail(c.Request.Context(), username)
	}
	if err != nil || !crypto.VerifyPassword(password, u.Password) || !u.IsActive {
		// Form POST → full-page navigation. Returning a plain string would
		// dump raw text in the browser. Bounce back to the configured login
		// UI with the original OIDC params + `error=invalid_credentials`
		// so the SPA can render a styled error and let the user retry.
		s.redirectLoginError(c, consts.LoginErrorInvalidCredentials, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
		return
	}
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(u.ID))

	code, err := randomToken(24)
	if err != nil {
		s.redirectLoginError(c, consts.LoginErrorInternal, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
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
		s.redirectLoginError(c, consts.LoginErrorInternal, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
		return
	}
	dest, err := buildRedirect(redirectURI, code, state)
	if err != nil {
		s.redirectLoginError(c, consts.LoginErrorInvalidRedirectURI, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod)
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

// token implements the OIDC token endpoint.
//
//	@Summary		OIDC token endpoint
//	@Description	OIDC token endpoint. Authenticates the client via Basic auth or `client_id`/`client_secret` form fields and dispatches on `grant_type` (`authorization_code`, `refresh_token`, `client_credentials`, `password`).
//	@Tags			OIDC
//	@ID				oidc_token
//	@Accept			x-www-form-urlencoded
//	@Produce		json
//	@Param			grant_type		formData	string	true	"OAuth2 grant type"
//	@Param			code			formData	string	false	"Authorization code (for `authorization_code`)"
//	@Param			redirect_uri	formData	string	false	"Redirect URI used at /authorize"
//	@Param			code_verifier	formData	string	false	"PKCE code verifier"
//	@Param			refresh_token	formData	string	false	"Refresh token (for `refresh_token`)"
//	@Param			username		formData	string	false	"Username (for `password`)"
//	@Param			password		formData	string	false	"Password (for `password`)"
//	@Param			client_id		formData	string	false	"Client id (when not using Basic auth)"
//	@Param			client_secret	formData	string	false	"Client secret (when not using Basic auth)"
//	@Success		200	{object}	tokenResp	"Token response"
//	@Failure		400	{object}	map[string]string	"OIDC error response"
//	@Failure		401	{object}	map[string]string	"Client authentication failed"
//	@Failure		500	{object}	map[string]string	"Internal server error"
//	@Router			/token [post]
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

// userinfo implements the OIDC userinfo endpoint.
//
//	@Summary		OIDC userinfo
//	@Description	OIDC userinfo endpoint. Validates the bearer access token via the SSO signer and returns standard claims (`sub`, `preferred_username`, `email`, `email_verified`, `name`, `picture`).
//	@Tags			OIDC
//	@ID				oidc_userinfo
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	map[string]any		"Userinfo claims"
//	@Failure		401	{object}	map[string]string	"Invalid token"
//	@Failure		404	{object}	map[string]string	"User not found"
//	@Router			/userinfo [get]
func (s *OIDCService) userinfo(c *gin.Context) {
	_, span := otel.Tracer(iamTracerName).Start(c.Request.Context(), "iam/sso/userinfo")
	defer span.End()
	token, err := crypto.ExtractTokenFromHeader(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return
	}
	claims, err := crypto.ParseToken(token, func(kid string) (*rsa.PublicKey, error) {
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

// logout invalidates the supplied refresh token.
//
//	@Summary		OIDC end-session
//	@Description	End-session endpoint advertised in discovery. When `refresh_token` is supplied it is removed from the SSO refresh store; access tokens remain valid until expiry.
//	@Tags			OIDC
//	@ID				oidc_logout
//	@Accept			x-www-form-urlencoded
//	@Produce		json
//	@Param			refresh_token	formData	string	false	"Refresh token to invalidate"
//	@Success		200	{object}	map[string]string	"Logout acknowledged"
//	@Router			/v1/logout [post]
func (s *OIDCService) logout(c *gin.Context) {
	_, span := otel.Tracer(iamTracerName).Start(c.Request.Context(), "iam/sso/logout")
	defer span.End()
	rt := c.PostForm("refresh_token")
	if rt != "" {
		_, _ = s.redis.DeleteKey(c.Request.Context(), refreshRedisPrefix+rt)
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// redirectLoginError 302-bounces back to the configured `sso.login_redirect`
// console URL with the original OIDC params + an `error` query so the SPA
// can render a styled error and let the user retry without losing context.
// Falls back to a plain text response when no login_redirect is configured
// (legacy inline-form mode).
func (s *OIDCService) redirectLoginError(c *gin.Context, errCode, clientID, redirectURI, state, scope, codeChallenge, codeChallengeMethod string) {
	dest := config.GetString("sso.login_redirect")
	if dest == "" {
		c.String(http.StatusUnauthorized, errCode)
		return
	}
	u, err := url.Parse(dest)
	if err != nil {
		c.String(http.StatusUnauthorized, errCode)
		return
	}
	q := u.Query()
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("scope", scope)
	q.Set("response_type", consts.OIDCResponseTypeCode)
	if codeChallenge != "" {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", codeChallengeMethod)
	}
	q.Set("error", errCode)
	u.RawQuery = q.Encode()
	c.Redirect(http.StatusFound, u.String())
}
