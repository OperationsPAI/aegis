package sso

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	federationStatePrefix = "sso:fedstate:"
	federationStateTTL    = 10 * time.Minute
)

type FederationState struct {
	Provider    string `json:"provider"`
	RedirectURI string `json:"redirect_uri"`
}

func (s *OIDCService) StoreFederationState(ctx context.Context, state string, fs FederationState) error {
	body, err := json.Marshal(fs)
	if err != nil {
		return err
	}
	return s.redis.Set(ctx, federationStatePrefix+state, string(body), federationStateTTL)
}

func (s *OIDCService) ValidateFederationState(ctx context.Context, state string) (*FederationState, error) {
	key := federationStatePrefix + state
	raw, err := s.redis.GetString(ctx, key)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, errors.New("federation state unknown or expired")
	}
	var fs FederationState
	if err := json.Unmarshal([]byte(raw), &fs); err != nil {
		return nil, err
	}
	_, _ = s.redis.DeleteKey(ctx, key)
	return &fs, nil
}
