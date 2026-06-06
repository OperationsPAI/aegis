package sso

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type IdentityProviderRepository struct {
	db *gorm.DB
}

func NewIdentityProviderRepository(db *gorm.DB) *IdentityProviderRepository {
	return &IdentityProviderRepository{db: db}
}

func (r *IdentityProviderRepository) Create(p *IdentityProvider) error {
	return r.db.Create(p).Error
}

func (r *IdentityProviderRepository) GetByID(id int) (*IdentityProvider, error) {
	var p IdentityProvider
	if err := r.db.Where("id = ?", id).First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, gorm.ErrRecordNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (r *IdentityProviderRepository) ListAll() ([]IdentityProvider, error) {
	var out []IdentityProvider
	if err := r.db.Order("id ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *IdentityProviderRepository) Update(p *IdentityProvider) error {
	return r.db.Save(p).Error
}

func (r *IdentityProviderRepository) Delete(id int) error {
	res := r.db.Delete(&IdentityProvider{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

type FederationAdminHandler struct {
	repo *IdentityProviderRepository
}

func NewFederationAdminHandler(repo *IdentityProviderRepository) *FederationAdminHandler {
	return &FederationAdminHandler{repo: repo}
}

type identityProviderResp struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Type          string `json:"type"`
	ClientID      string `json:"client_id"`
	DiscoveryURL  string `json:"discovery_url,omitempty"`
	AuthorizeURL  string `json:"authorize_url,omitempty"`
	TokenURL      string `json:"token_url,omitempty"`
	UserinfoURL   string `json:"userinfo_url,omitempty"`
	Scopes        string `json:"scopes"`
	AutoProvision bool   `json:"auto_provision"`
	Enabled       bool   `json:"enabled"`
}

func toResp(p *IdentityProvider) identityProviderResp {
	return identityProviderResp{
		ID: p.ID, Name: p.Name, DisplayName: p.DisplayName, Type: p.Type,
		ClientID: p.ClientID, DiscoveryURL: p.DiscoveryURL,
		AuthorizeURL: p.AuthorizeURL, TokenURL: p.TokenURL,
		UserinfoURL: p.UserinfoURL, Scopes: p.Scopes,
		AutoProvision: p.AutoProvision, Enabled: p.Enabled,
	}
}

type createProviderReq struct {
	Name          string `json:"name" binding:"required"`
	DisplayName   string `json:"display_name" binding:"required"`
	Type          string `json:"type" binding:"omitempty,oneof=oidc oauth2"`
	ClientID      string `json:"client_id" binding:"required"`
	ClientSecret  string `json:"client_secret" binding:"required"`
	DiscoveryURL  string `json:"discovery_url"`
	AuthorizeURL  string `json:"authorize_url"`
	TokenURL      string `json:"token_url"`
	UserinfoURL   string `json:"userinfo_url"`
	Scopes        string `json:"scopes"`
	AutoProvision *bool  `json:"auto_provision"`
	DefaultRoles  string `json:"default_roles"`
}

type updateProviderReq struct {
	DisplayName   *string `json:"display_name"`
	ClientID      *string `json:"client_id"`
	ClientSecret  *string `json:"client_secret"`
	DiscoveryURL  *string `json:"discovery_url"`
	AuthorizeURL  *string `json:"authorize_url"`
	TokenURL      *string `json:"token_url"`
	UserinfoURL   *string `json:"userinfo_url"`
	Scopes        *string `json:"scopes"`
	AutoProvision *bool   `json:"auto_provision"`
	DefaultRoles  *string `json:"default_roles"`
	Enabled       *bool   `json:"enabled"`
}

// applyProviderPreset fills well-known endpoint fields for google/github when
// the admin left them empty, so only client_id/client_secret are required.
// Explicitly supplied values are never overridden.
func applyProviderPreset(req *createProviderReq) {
	type preset struct {
		typ          string
		discoveryURL string
		authorizeURL string
		tokenURL     string
		userinfoURL  string
		scopes       string
	}
	presets := map[string]preset{
		"google": {
			typ:          "oidc",
			discoveryURL: "https://accounts.google.com/.well-known/openid-configuration",
			authorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
			tokenURL:     "https://oauth2.googleapis.com/token",
			scopes:       "openid,email,profile",
		},
		"github": {
			typ:          "oauth2",
			authorizeURL: "https://github.com/login/oauth/authorize",
			tokenURL:     "https://github.com/login/oauth/access_token",
			userinfoURL:  "https://api.github.com/user",
			scopes:       "read:user,user:email",
		},
	}
	p, ok := presets[strings.ToLower(req.Name)]
	if !ok {
		return
	}
	if req.Type == "" {
		req.Type = p.typ
	}
	if req.DiscoveryURL == "" {
		req.DiscoveryURL = p.discoveryURL
	}
	if req.AuthorizeURL == "" {
		req.AuthorizeURL = p.authorizeURL
	}
	if req.TokenURL == "" {
		req.TokenURL = p.tokenURL
	}
	if req.UserinfoURL == "" {
		req.UserinfoURL = p.userinfoURL
	}
	if req.Scopes == "" {
		req.Scopes = p.scopes
	}
}

//	@Summary		Create identity provider
//	@Tags			SSO Admin
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		createProviderReq	true	"Provider config"
//	@Success		201		{object}	object
//	@Router			/v1/identity-providers [post]
func (h *FederationAdminHandler) CreateProvider(c *gin.Context) {
	var req createProviderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	applyProviderPreset(&req)
	if req.Type == "" {
		dto.ErrorResponse(c, http.StatusBadRequest, "type is required (no preset for this provider name)")
		return
	}
	ap := true
	if req.AutoProvision != nil {
		ap = *req.AutoProvision
	}
	p := &IdentityProvider{
		Name: req.Name, DisplayName: req.DisplayName, Type: req.Type,
		ClientID: req.ClientID, ClientSecret: req.ClientSecret,
		DiscoveryURL: req.DiscoveryURL, AuthorizeURL: req.AuthorizeURL,
		TokenURL: req.TokenURL, UserinfoURL: req.UserinfoURL,
		Scopes: req.Scopes, AutoProvision: ap,
		DefaultRoles: req.DefaultRoles, Enabled: true,
	}
	if err := h.repo.Create(p); err != nil {
		if strings.Contains(err.Error(), "Duplicate") {
			dto.ErrorResponse(c, http.StatusConflict, "provider name already exists")
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to create provider")
		return
	}
	r := toResp(p)
	dto.JSONResponse(c, http.StatusCreated, "created", r)
}

//	@Summary		List identity providers (admin)
//	@Tags			SSO Admin
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	object
//	@Router			/v1/identity-providers [get]
func (h *FederationAdminHandler) ListProviders(c *gin.Context) {
	providers, err := h.repo.ListAll()
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to list providers")
		return
	}
	out := make([]identityProviderResp, len(providers))
	for i := range providers {
		out[i] = toResp(&providers[i])
	}
	dto.SuccessResponse(c, out)
}

//	@Summary		Get identity provider
//	@Tags			SSO Admin
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path	int	true	"Provider ID"
//	@Success		200	{object}	object
//	@Failure		404	{object}	object
//	@Router			/v1/identity-providers/{id} [get]
func (h *FederationAdminHandler) GetProvider(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		dto.ErrorResponse(c, http.StatusBadRequest, "invalid id")
		return
	}
	p, err := h.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			dto.ErrorResponse(c, http.StatusNotFound, "not found")
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to get provider")
		return
	}
	dto.SuccessResponse(c, toResp(p))
}

//	@Summary		Update identity provider
//	@Tags			SSO Admin
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path	int					true	"Provider ID"
//	@Param			request	body	updateProviderReq	true	"Update"
//	@Success		200		{object}	object
//	@Router			/v1/identity-providers/{id} [put]
func (h *FederationAdminHandler) UpdateProvider(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		dto.ErrorResponse(c, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateProviderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	p, err := h.repo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			dto.ErrorResponse(c, http.StatusNotFound, "not found")
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed")
		return
	}
	if req.DisplayName != nil {
		p.DisplayName = *req.DisplayName
	}
	if req.ClientID != nil {
		p.ClientID = *req.ClientID
	}
	if req.ClientSecret != nil {
		p.ClientSecret = *req.ClientSecret
	}
	if req.DiscoveryURL != nil {
		p.DiscoveryURL = *req.DiscoveryURL
	}
	if req.AuthorizeURL != nil {
		p.AuthorizeURL = *req.AuthorizeURL
	}
	if req.TokenURL != nil {
		p.TokenURL = *req.TokenURL
	}
	if req.UserinfoURL != nil {
		p.UserinfoURL = *req.UserinfoURL
	}
	if req.Scopes != nil {
		p.Scopes = *req.Scopes
	}
	if req.AutoProvision != nil {
		p.AutoProvision = *req.AutoProvision
	}
	if req.DefaultRoles != nil {
		p.DefaultRoles = *req.DefaultRoles
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if err := h.repo.Update(p); err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to update provider")
		return
	}
	dto.SuccessResponse(c, toResp(p))
}

//	@Summary		Delete identity provider
//	@Tags			SSO Admin
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path	int	true	"Provider ID"
//	@Success		200	{object}	object
//	@Failure		404	{object}	object
//	@Router			/v1/identity-providers/{id} [delete]
func (h *FederationAdminHandler) DeleteProvider(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		dto.ErrorResponse(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.repo.Delete(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			dto.ErrorResponse(c, http.StatusNotFound, "not found")
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to delete provider")
		return
	}
	dto.SuccessResponse(c, gin.H{"id": id})
}

func RegisterFederationAdminRoutes(engine *gin.Engine, h *FederationAdminHandler) {
	g := engine.Group("/v1/identity-providers", middleware.JWTAuth())
	{
		g.POST("", h.CreateProvider)
		g.GET("", h.ListProviders)
		g.GET("/:id", h.GetProvider)
		g.PUT("/:id", h.UpdateProvider)
		g.DELETE("/:id", h.DeleteProvider)
	}
}
