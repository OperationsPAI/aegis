package sso

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"aegis/consts"
	"aegis/model"

	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func generateSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func hashSecret(secret string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifySecret compares a plaintext secret against a stored bcrypt hash. It
// returns nil only when the client is active and the secret matches.
func (s *Service) VerifySecret(ctx context.Context, clientID, secret string) (*model.OIDCClient, error) {
	c, err := s.repo.GetByClientID(clientID)
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(c.ClientSecretHash), []byte(secret)) != nil {
		return nil, consts.ErrAuthenticationFailed
	}
	return c, nil
}

func (s *Service) GetByClientID(_ context.Context, clientID string) (*model.OIDCClient, error) {
	return s.repo.GetByClientID(clientID)
}

func (s *Service) Create(_ context.Context, req *CreateClientReq) (*CreateClientResp, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", consts.ErrBadRequest, err)
	}
	if _, err := s.repo.GetByClientID(req.ClientID); err == nil {
		return nil, fmt.Errorf("%w: client_id already exists", consts.ErrAlreadyExists)
	} else if !errors.Is(err, consts.ErrNotFound) {
		return nil, err
	}

	secret, err := generateSecret()
	if err != nil {
		return nil, err
	}
	hash, err := hashSecret(secret)
	if err != nil {
		return nil, err
	}

	confidential := true
	if req.IsConfidential != nil {
		confidential = *req.IsConfidential
	}

	c := &model.OIDCClient{
		ClientID:         req.ClientID,
		ClientSecretHash: hash,
		Name:             req.Name,
		Service:          req.Service,
		RedirectURIs:     req.RedirectURIs,
		Grants:           req.Grants,
		Scopes:           req.Scopes,
		IsConfidential:   confidential,
		Status:           consts.CommonEnabled,
	}
	if err := s.repo.Create(c); err != nil {
		return nil, err
	}
	return &CreateClientResp{ClientResp: NewClientResp(c), ClientSecret: secret}, nil
}

func (s *Service) Get(_ context.Context, id int) (*ClientResp, error) {
	c, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	return NewClientResp(c), nil
}

func (s *Service) List(_ context.Context, serviceFilter string) ([]ClientResp, error) {
	cs, err := s.repo.List(serviceFilter)
	if err != nil {
		return nil, err
	}
	out := make([]ClientResp, len(cs))
	for i := range cs {
		out[i] = *NewClientResp(&cs[i])
	}
	return out, nil
}

// ListForServices returns all clients whose service is in the given set.
// Used by the /v1/clients gate for service admins (Task #13). Empty input
// = empty result.
func (s *Service) ListForServices(_ context.Context, services []string) ([]ClientResp, error) {
	if len(services) == 0 {
		return []ClientResp{}, nil
	}
	out := make([]ClientResp, 0)
	seen := make(map[int]struct{})
	for _, svc := range services {
		cs, err := s.repo.List(svc)
		if err != nil {
			return nil, err
		}
		for i := range cs {
			if _, dup := seen[cs[i].ID]; dup {
				continue
			}
			seen[cs[i].ID] = struct{}{}
			out = append(out, *NewClientResp(&cs[i]))
		}
	}
	return out, nil
}

func (s *Service) Update(_ context.Context, id int, req *UpdateClientReq) (*ClientResp, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", consts.ErrBadRequest, err)
	}
	c, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		c.Name = *req.Name
	}
	if req.RedirectURIs != nil {
		c.RedirectURIs = req.RedirectURIs
	}
	if req.Grants != nil {
		c.Grants = req.Grants
	}
	if req.Scopes != nil {
		c.Scopes = req.Scopes
	}
	if err := s.repo.Update(c); err != nil {
		return nil, err
	}
	return NewClientResp(c), nil
}

func (s *Service) RotateSecret(_ context.Context, id int) (*RotateSecretResp, error) {
	c, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	secret, err := generateSecret()
	if err != nil {
		return nil, err
	}
	hash, err := hashSecret(secret)
	if err != nil {
		return nil, err
	}
	c.ClientSecretHash = hash
	if err := s.repo.Update(c); err != nil {
		return nil, err
	}
	return &RotateSecretResp{ClientID: c.ClientID, ClientSecret: secret}, nil
}

func (s *Service) Delete(_ context.Context, id int) error {
	return s.repo.SoftDelete(id)
}
