package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aegis/crud/iam/rbac"
	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/dto"
	"aegis/platform/model"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

type FederationHandler struct {
	repo  *FederationRepository
	oidc  *OIDCService
	roles *rbac.Repository
}

func NewFederationHandler(repo *FederationRepository, oidc *OIDCService, roles *rbac.Repository) *FederationHandler {
	return &FederationHandler{repo: repo, oidc: oidc, roles: roles}
}

func RegisterFederationRoutes(engine *gin.Engine, h *FederationHandler) {
	g := engine.Group("/auth")
	{
		g.GET("/providers", h.ListProviders)
		g.GET("/federated/:provider", h.Authorize)
		g.GET("/callback/:provider", h.Callback)
	}
}

type providerInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
}

// ListProviders returns enabled identity providers without secrets.
func (h *FederationHandler) ListProviders(c *gin.Context) {
	providers, err := h.repo.ListEnabledProviders(c.Request.Context())
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to list providers")
		return
	}
	out := make([]providerInfo, len(providers))
	for i, p := range providers {
		out[i] = providerInfo{
			Name:        p.Name,
			DisplayName: p.DisplayName,
			Type:        p.Type,
		}
	}
	dto.SuccessResponse(c, out)
}

// Authorize redirects the user to the external IdP.
//
//	@Summary		Federated login: redirect to IdP
//	@Description	Starts a federated login by redirecting to the external IdP. When `client_id` is supplied, the original console OIDC authorization-code params are carried across the IdP round-trip so the callback can mint an auth code and 302 back to the console `redirect_uri?code&state` (PKCE exchange follows). Without `client_id`, the callback returns the token as JSON (direct API/CLI use).
//	@Tags			SSO Federation
//	@Produce		html
//	@Param			provider				path		string	true	"Provider name (e.g. google, github)"
//	@Param			client_id				query		string	false	"Console OIDC client_id (enables the auth-code bridge)"
//	@Param			redirect_uri			query		string	false	"Console OIDC redirect_uri (where the auth code is delivered)"
//	@Param			state					query		string	false	"Console OIDC state echoed back to redirect_uri"
//	@Param			scope					query		string	false	"Console OIDC requested scope"
//	@Param			code_challenge			query		string	false	"PKCE code challenge"
//	@Param			code_challenge_method	query		string	false	"PKCE method (`S256` or `plain`)"
//	@Success		302	{string}	string	"Redirect to external IdP"
//	@Failure		400	{object}	object	"Invalid console OIDC request"
//	@Failure		404	{object}	object	"Provider not found"
//	@Router			/auth/federated/{provider} [get]
func (h *FederationHandler) Authorize(c *gin.Context) {
	providerName := c.Param("provider")
	idp, err := h.repo.FindProviderByName(c.Request.Context(), providerName)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			dto.ErrorResponse(c, http.StatusNotFound, "provider not found")
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to load provider")
		return
	}
	if !idp.Enabled {
		dto.ErrorResponse(c, http.StatusForbidden, "provider is disabled")
		return
	}

	clientID := c.Query("client_id")
	redirectURI := c.Query("redirect_uri")
	if clientID != "" {
		cli, err := h.oidc.clients.GetByClientID(c.Request.Context(), clientID)
		if err != nil {
			dto.ErrorResponse(c, http.StatusBadRequest, "unknown client_id")
			return
		}
		if !grantAllowed(cli, consts.OIDCGrantAuthorizationCode) {
			dto.ErrorResponse(c, http.StatusBadRequest, "authorization_code grant not allowed for client")
			return
		}
		if !redirectAllowed(cli, redirectURI) {
			dto.ErrorResponse(c, http.StatusBadRequest, "redirect_uri not allowed for client")
			return
		}
		if !cli.IsConfidential && c.Query("code_challenge") == "" {
			dto.ErrorResponse(c, http.StatusBadRequest, "code_challenge required for public client")
			return
		}
	}

	oc := h.buildOAuth2Config(idp, c.Request)
	state, err := randomToken(16)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to generate state")
		return
	}

	if err := h.oidc.StoreFederationState(c.Request.Context(), state, FederationState{
		Provider:            providerName,
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		State:               c.Query("state"),
		Scope:               c.Query("scope"),
		CodeChallenge:       c.Query("code_challenge"),
		CodeChallengeMethod: c.Query("code_challenge_method"),
	}); err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to store state")
		return
	}

	url := oc.AuthCodeURL(state)
	c.Redirect(http.StatusFound, url)
}

// Callback handles the OAuth2 callback from the external IdP.
func (h *FederationHandler) Callback(c *gin.Context) {
	providerName := c.Param("provider")
	stateParam := c.Query("state")
	code := c.Query("code")

	if stateParam == "" || code == "" {
		dto.ErrorResponse(c, http.StatusBadRequest, "missing state or code")
		return
	}

	fs, err := h.oidc.ValidateFederationState(c.Request.Context(), stateParam)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "invalid or expired state")
		return
	}
	if fs.Provider != providerName {
		dto.ErrorResponse(c, http.StatusBadRequest, "state provider mismatch")
		return
	}

	idp, err := h.repo.FindProviderByName(c.Request.Context(), providerName)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to load provider")
		return
	}

	oc := h.buildOAuth2Config(idp, c.Request)
	token, err := oc.Exchange(c.Request.Context(), code)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadGateway, "token exchange failed")
		return
	}

	sub, email, err := h.extractIdentity(c.Request.Context(), idp, token)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadGateway, "failed to extract identity: "+err.Error())
		return
	}

	u, err := h.resolveUser(c.Request.Context(), idp, providerName, sub, email)
	if err != nil {
		if errors.Is(err, consts.ErrPermissionDenied) {
			dto.ErrorResponse(c, http.StatusForbidden, "auto-provisioning is disabled for this provider")
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to provision user")
		return
	}

	// Persist the provider access token so authenticated proxy endpoints
	// (e.g. the GitHub repo proxy) can call the provider API on the user's
	// behalf. Best-effort: the identity exists for both freshly-provisioned
	// and returning users at this point, and a storage failure must not fail
	// the login.
	if meta, mErr := marshalTokenMetadata(token); mErr == nil {
		if identity, fErr := h.repo.FindIdentity(c.Request.Context(), providerName, sub); fErr == nil {
			if uErr := h.repo.UpdateIdentityMetadata(c.Request.Context(), identity.ID, meta); uErr != nil {
				logrus.WithError(uErr).Warn("federation: failed to persist provider token metadata")
			}
		}
	}

	if fs.ClientID != "" {
		authCode, err := randomToken(24)
		if err != nil {
			dto.ErrorResponse(c, http.StatusInternalServerError, "failed to generate code")
			return
		}
		ar := authRequest{
			ClientID:            fs.ClientID,
			UserID:              u.ID,
			RedirectURI:         fs.RedirectURI,
			State:               fs.State,
			Scope:               fs.Scope,
			CodeChallenge:       fs.CodeChallenge,
			CodeChallengeMethod: fs.CodeChallengeMethod,
			Idp:                 providerName,
		}
		if err := h.oidc.storeAuthRequest(c.Request.Context(), authCode, ar); err != nil {
			dto.ErrorResponse(c, http.StatusInternalServerError, "failed to store auth request")
			return
		}
		dest, err := buildRedirect(fs.RedirectURI, authCode, fs.State)
		if err != nil {
			dto.ErrorResponse(c, http.StatusBadRequest, "invalid redirect_uri")
			return
		}
		c.Redirect(http.StatusFound, dest)
		return
	}

	accessToken, expiresIn, err := h.mintUserToken(c.Request.Context(), u, providerName)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to mint token")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token": accessToken,
		"token_type":   consts.TokenTypeBearer,
		"expires_in":   expiresIn,
	})
}

// federatedTokenMetadata is the JSON shape persisted in UserIdentity.Metadata
// for oauth2 providers. The GitHub proxy handler reads AccessToken back out.
type federatedTokenMetadata struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry"`
}

func marshalTokenMetadata(token *oauth2.Token) (string, error) {
	b, err := json.Marshal(federatedTokenMetadata{
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry,
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (h *FederationHandler) buildOAuth2Config(idp *IdentityProvider, r *http.Request) *oauth2.Config {
	scopes := strings.Split(idp.Scopes, ",")
	for i := range scopes {
		scopes[i] = strings.TrimSpace(scopes[i])
	}

	authURL := idp.AuthorizeURL
	tokenURL := idp.TokenURL
	if idp.Type == "oidc" && idp.DiscoveryURL != "" && (authURL == "" || tokenURL == "") {
		// For OIDC providers with a discovery URL but missing explicit endpoints,
		// derive standard endpoint paths from the issuer.
		base := strings.TrimSuffix(idp.DiscoveryURL, "/.well-known/openid-configuration")
		if authURL == "" {
			authURL = base + "/authorize"
		}
		if tokenURL == "" {
			tokenURL = base + "/token"
		}
	}

	// Behind a TLS-terminating reverse proxy r.TLS is nil and r.Host is the
	// internal upstream address, so an inferred callback would not match the
	// public redirect_uri registered with the IdP. Prefer an explicitly
	// configured public base (required when the proxy rewrites the port, e.g.
	// CLB NAT), then the X-Forwarded-* hints, then the raw request.
	base := strings.TrimRight(config.GetString("sso.federation.callback_base_url"), "/")
	if base == "" {
		scheme := r.Header.Get("X-Forwarded-Proto")
		if scheme == "" {
			scheme = "http"
			if r.TLS != nil {
				scheme = "https"
			}
		}
		host := r.Header.Get("X-Forwarded-Host")
		if host == "" {
			host = r.Host
		}
		base = scheme + "://" + host
	}
	redirectURL := fmt.Sprintf("%s/auth/callback/%s", base, idp.Name)

	return &oauth2.Config{
		ClientID:     idp.ClientID,
		ClientSecret: idp.ClientSecret,
		Scopes:       scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
		RedirectURL: redirectURL,
	}
}

func (h *FederationHandler) extractIdentity(ctx context.Context, idp *IdentityProvider, token *oauth2.Token) (sub, email string, err error) {
	if idp.Type == "oidc" {
		return h.extractFromIDToken(token)
	}
	return h.extractFromUserinfo(ctx, idp, token)
}

// extractFromIDToken parses the id_token JWT without signature verification
// (we already trust the token endpoint response) to extract sub and email.
func (h *FederationHandler) extractFromIDToken(token *oauth2.Token) (sub, email string, err error) {
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", "", errors.New("no id_token in token response")
	}

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsed, _, err := parser.ParseUnverified(rawIDToken, jwt.MapClaims{})
	if err != nil {
		return "", "", fmt.Errorf("parse id_token: %w", err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", errors.New("unexpected claims type")
	}

	if s, ok := claims["sub"].(string); ok {
		sub = s
	}
	if e, ok := claims["email"].(string); ok {
		email = e
	}
	if sub == "" {
		return "", "", errors.New("id_token missing sub claim")
	}
	return sub, email, nil
}

// extractFromUserinfo calls the IdP's userinfo endpoint (e.g. GitHub's /user)
// to get sub and email.
func (h *FederationHandler) extractFromUserinfo(ctx context.Context, idp *IdentityProvider, token *oauth2.Token) (sub, email string, err error) {
	if idp.UserinfoURL == "" {
		return "", "", errors.New("userinfo_url not configured for oauth2 provider")
	}

	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
	resp, err := client.Get(idp.UserinfoURL)
	if err != nil {
		return "", "", fmt.Errorf("userinfo request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read userinfo response: %w", err)
	}

	var info map[string]any
	if err := json.Unmarshal(body, &info); err != nil {
		return "", "", fmt.Errorf("parse userinfo: %w", err)
	}

	// GitHub uses numeric "id"; standard OIDC uses string "sub".
	if s, ok := info["sub"].(string); ok && s != "" {
		sub = s
	} else if id, ok := info["id"]; ok {
		// GitHub returns a numeric id; JSON-decoded into any it is a float64,
		// so fmt.Sprint would yield scientific notation ("5.3e+07").
		switch v := id.(type) {
		case float64:
			sub = strconv.FormatInt(int64(v), 10)
		case json.Number:
			sub = v.String()
		case string:
			sub = v
		default:
			sub = fmt.Sprint(v)
		}
	}
	if e, ok := info["email"].(string); ok {
		email = e
	}
	if sub == "" {
		return "", "", errors.New("userinfo missing sub/id")
	}
	return sub, email, nil
}

func (h *FederationHandler) resolveUser(ctx context.Context, idp *IdentityProvider, providerName, sub, email string) (*model.User, error) {
	identity, err := h.repo.FindIdentity(ctx, providerName, sub)
	if err != nil && !errors.Is(err, consts.ErrNotFound) {
		return nil, err
	}

	if identity != nil {
		u, err := h.oidc.users.GetByID(ctx, identity.UserID)
		if err != nil {
			return nil, fmt.Errorf("load linked user: %w", err)
		}
		_ = h.repo.UpdateLastUsed(ctx, identity.ID)
		return u, nil
	}

	if !idp.AutoProvision {
		return nil, consts.ErrPermissionDenied
	}
	return h.provisionUser(ctx, idp, providerName, sub, email)
}

func (h *FederationHandler) mintUserToken(ctx context.Context, u *model.User, providerName string) (accessToken string, expiresIn int64, err error) {
	roles, _ := h.oidc.users.ListRoleNames(ctx, u.ID)
	isAdmin := crypto.IsAdminRole(roles)

	signed, expiresAt, err := crypto.GenerateUnifiedToken(crypto.UnifiedTokenParams{
		Typ: "human", UserID: u.ID, Username: u.Username, Email: u.Email,
		IsActive: u.IsActive, IsAdmin: isAdmin, Roles: roles,
		AuthType: "user", Idp: providerName,
		Lifetime: crypto.TokenExpiration, Audience: crypto.AudienceForHuman(isAdmin),
	}, h.oidc.signer.PrivateKey, h.oidc.signer.Kid)
	if err != nil {
		return "", 0, fmt.Errorf("mint token: %w", err)
	}
	return signed, int64(time.Until(expiresAt).Seconds()), nil
}

func (h *FederationHandler) provisionUser(ctx context.Context, idp *IdentityProvider, providerName, sub, email string) (*model.User, error) {
	username := email
	if at := strings.Index(email, "@"); at > 0 {
		username = email[:at]
	}
	if username == "" {
		username = providerName + "_" + sub
	}
	// Email is NOT NULL + UNIQUE, but IdPs hide it (GitHub private email):
	// synthesize a deterministic per-identity placeholder so provisioning
	// never fails and stays unique across federated users.
	if email == "" {
		email = username + "@" + providerName + ".noreply"
	}

	password, err := randomToken(16)
	if err != nil {
		return nil, fmt.Errorf("generate password: %w", err)
	}

	u := &model.User{
		Username: username,
		Email:    email,
		Password: password,
		FullName: username,
		Status:   consts.CommonEnabled,
		IsActive: true,
	}

	// active_username is a generated column; Omit it so the INSERT is valid.
	// User.BeforeCreate hook hashes the password.
	created := true
	if err := h.repo.db.WithContext(ctx).Omit("active_username").Create(u).Error; err != nil {
		// Username/email collision: try to find the existing user by email
		// and link instead of failing.
		existing, findErr := h.oidc.users.GetByEmail(ctx, email)
		if findErr != nil {
			return nil, fmt.Errorf("create user: %w (also failed to find existing: %v)", err, findErr)
		}
		u = existing
		created = false
	}

	now := time.Now()
	identity := &UserIdentity{
		UserID:        u.ID,
		Provider:      providerName,
		ExternalSub:   sub,
		ExternalEmail: email,
		LinkedAt:      now,
		LastUsedAt:    &now,
	}
	if err := h.repo.LinkIdentity(ctx, identity); err != nil {
		return nil, fmt.Errorf("link identity: %w", err)
	}

	if created {
		h.assignDefaultRoles(ctx, u.ID, idp.DefaultRoles)
	}

	return u, nil
}

// assignDefaultRoles grants the provider's configured default global roles to a
// freshly provisioned federated user. Best-effort: a missing/misconfigured role
// is logged and skipped rather than failing the login.
func (h *FederationHandler) assignDefaultRoles(ctx context.Context, userID int, defaultRoles string) {
	for _, name := range strings.Split(defaultRoles, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		role, err := h.roles.FindRoleByName(name)
		if err != nil {
			logrus.WithError(err).Warnf("federation: default role %q not found, skipping", name)
			continue
		}
		if err := h.oidc.users.AssignRole(ctx, userID, role.ID); err != nil {
			logrus.WithError(err).Warnf("federation: failed to assign default role %q to user %d", name, userID)
		}
	}
}
