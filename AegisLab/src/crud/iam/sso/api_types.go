package sso

import (
	"errors"
	"time"

	"aegis/platform/consts"
	"aegis/platform/model"
)

type ClientResp struct {
	ID             int       `json:"id"`
	ClientID       string    `json:"client_id"`
	Name           string    `json:"name"`
	Service        string    `json:"service"`
	RedirectURIs   []string  `json:"redirect_uris"`
	Grants         []string  `json:"grants"`
	Scopes         []string  `json:"scopes"`
	IsConfidential bool      `json:"is_confidential"`
	Status         int       `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func NewClientResp(c *model.OIDCClient) *ClientResp {
	return &ClientResp{
		ID:             c.ID,
		ClientID:       c.ClientID,
		Name:           c.Name,
		Service:        c.Service,
		RedirectURIs:   append([]string(nil), c.RedirectURIs...),
		Grants:         append([]string(nil), c.Grants...),
		Scopes:         append([]string(nil), c.Scopes...),
		IsConfidential: c.IsConfidential,
		Status:         int(c.Status),
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
	}
}

type CreateClientReq struct {
	ClientID       string   `json:"client_id"`
	Name           string   `json:"name"`
	Service        string   `json:"service"`
	RedirectURIs   []string `json:"redirect_uris"`
	Grants         []string `json:"grants"`
	Scopes         []string `json:"scopes"`
	IsConfidential *bool    `json:"is_confidential,omitempty"`
}

func (r *CreateClientReq) Validate() error {
	if r.ClientID == "" {
		return errors.New("client_id is required")
	}
	if r.Name == "" {
		return errors.New("name is required")
	}
	if r.Service == "" {
		return errors.New("service is required")
	}
	if len(r.Grants) == 0 {
		return errors.New("grants must not be empty")
	}
	for _, g := range r.Grants {
		if !isAllowedGrant(g) {
			return errors.New("unsupported grant: " + g)
		}
	}
	return nil
}

type UpdateClientReq struct {
	Name         *string  `json:"name,omitempty"`
	RedirectURIs []string `json:"redirect_uris,omitempty"`
	Grants       []string `json:"grants,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

func (r *UpdateClientReq) Validate() error {
	for _, g := range r.Grants {
		if !isAllowedGrant(g) {
			return errors.New("unsupported grant: " + g)
		}
	}
	return nil
}

type CreateClientResp struct {
	*ClientResp
	ClientSecret string `json:"client_secret"`
}

type RotateSecretResp struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

var allowedGrants = map[string]struct{}{
	consts.OIDCGrantAuthorizationCode: {},
	consts.OIDCGrantRefreshToken:      {},
	consts.OIDCGrantClientCredentials: {},
	consts.OIDCGrantPassword:          {},
}

func isAllowedGrant(g string) bool {
	_, ok := allowedGrants[g]
	return ok
}
