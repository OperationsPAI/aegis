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

// FederationState bridges a console-initiated OIDC authorization-code flow
// across the IdP round-trip. When ClientID is set, Callback mints an auth code
// for the console's redirect_uri instead of returning a token as JSON.
type FederationState struct {
	Provider            string `json:"provider"`
	ClientID            string `json:"client_id,omitempty"`
	RedirectURI         string `json:"redirect_uri,omitempty"`
	State               string `json:"state,omitempty"`
	Scope               string `json:"scope,omitempty"`
	CodeChallenge       string `json:"code_challenge,omitempty"`
	CodeChallengeMethod string `json:"code_challenge_method,omitempty"`
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
