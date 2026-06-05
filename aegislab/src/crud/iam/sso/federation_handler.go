package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/dto"
	"aegis/platform/model"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

type FederationHandler struct {
	repo *FederationRepository
	oidc *OIDCService
}

func NewFederationHandler(repo *FederationRepository, oidc *OIDCService) *FederationHandler {
	return &FederationHandler{repo: repo, oidc: oidc}
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

	oc := h.buildOAuth2Config(idp, c.Request)
	state, err := randomToken(16)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to generate state")
		return
	}

	redirectURI := c.Query("redirect_uri")
	if err := h.oidc.StoreFederationState(c.Request.Context(), state, FederationState{
		Provider:    providerName,
		RedirectURI: redirectURI,
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

	accessToken, expiresIn, err := h.findOrProvisionUser(c.Request.Context(), idp, providerName, sub, email)
	if err != nil {
		if errors.Is(err, consts.ErrPermissionDenied) {
			dto.ErrorResponse(c, http.StatusForbidden, "auto-provisioning is disabled for this provider")
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to provision user")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token": accessToken,
		"token_type":   consts.TokenTypeBearer,
		"expires_in":   expiresIn,
	})
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

	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	redirectURL := fmt.Sprintf("%s://%s/auth/callback/%s", scheme, r.Host, idp.Name)

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
		sub = fmt.Sprint(id)
	}
	if e, ok := info["email"].(string); ok {
		email = e
	}
	if sub == "" {
		return "", "", errors.New("userinfo missing sub/id")
	}
	return sub, email, nil
}

func (h *FederationHandler) findOrProvisionUser(ctx context.Context, idp *IdentityProvider, providerName, sub, email string) (accessToken string, expiresIn int64, err error) {
	identity, err := h.repo.FindIdentity(ctx, providerName, sub)
	if err != nil && !errors.Is(err, consts.ErrNotFound) {
		return "", 0, err
	}

	var u *model.User

	if identity != nil {
		u, err = h.oidc.users.GetByID(ctx, identity.UserID)
		if err != nil {
			return "", 0, fmt.Errorf("load linked user: %w", err)
		}
		_ = h.repo.UpdateLastUsed(ctx, identity.ID)
	} else {
		if !idp.AutoProvision {
			return "", 0, consts.ErrPermissionDenied
		}
		u, err = h.provisionUser(ctx, idp, providerName, sub, email)
		if err != nil {
			return "", 0, err
		}
	}

	roles, _ := h.oidc.users.ListRoleNames(ctx, u.ID)
	isAdmin := false
	for _, r := range roles {
		if r == string(consts.RoleSuperAdmin) || r == string(consts.RoleAdmin) {
			isAdmin = true
			break
		}
	}

	signed, expiresAt, err := crypto.GenerateUnifiedToken(crypto.UnifiedTokenParams{
		Typ: "human", UserID: u.ID, Username: u.Username, Email: u.Email,
		IsActive: u.IsActive, IsAdmin: isAdmin, Roles: roles,
		AuthType: "user", Idp: providerName,
		Lifetime: crypto.TokenExpiration, Audience: []string{"portal"},
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

	// User.BeforeCreate hook hashes the password.
	if err := h.repo.db.WithContext(ctx).Create(u).Error; err != nil {
		// Username/email collision: try to find the existing user by email
		// and link instead of failing.
		existing, findErr := h.oidc.users.GetByEmail(ctx, email)
		if findErr != nil {
			return nil, fmt.Errorf("create user: %w (also failed to find existing: %v)", err, findErr)
		}
		u = existing
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

	return u, nil
}
