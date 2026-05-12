package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"aegis/platform/consts"
	redis "aegis/platform/redis"
)

// tokenBlacklistPrefix keys every revoked token by its JWT ID (claims.ID).
// Blacklist additions set an EX TTL equal to the token's remaining lifetime,
// and lookups use EXISTS against this exact key — never SCAN MATCH —
// so the per-request auth cost stays O(1) regardless of blacklist size.
const tokenBlacklistPrefix = "blacklist:token:%s"
const apiKeyNoncePrefix = "api_key:nonce:%s:%s"

type TokenStore struct {
	redis *redis.Gateway
}

func NewTokenStore(redis *redis.Gateway) *TokenStore {
	return &TokenStore{redis: redis}
}

func (s *TokenStore) AddTokenToBlacklist(ctx context.Context, tokenID string, expiresAt time.Time, metaData map[string]any) error {
	key := fmt.Sprintf(tokenBlacklistPrefix, tokenID)

	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return nil
	}

	metaDataJSON, err := json.Marshal(metaData)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata to JSON: %w", err)
	}

	if err = s.redis.Set(ctx, key, string(metaDataJSON), ttl); err != nil {
		return fmt.Errorf("failed to blacklist token in Redis: %w", err)
	}

	return nil
}

func (s *TokenStore) ReserveAPIKeyNonce(ctx context.Context, keyID, nonce string, ttl time.Duration) error {
	if s == nil || s.redis == nil {
		return nil
	}

	key := fmt.Sprintf(apiKeyNoncePrefix, keyID, nonce)
	ok, err := s.redis.SetNX(ctx, key, "1", ttl)
	if err != nil {
		return fmt.Errorf("failed to reserve api key nonce: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: request nonce has already been used", consts.ErrAuthenticationFailed)
	}
	return nil
}

func (s *TokenStore) IsTokenBlacklisted(ctx context.Context, tokenID string) (bool, error) {
	if s == nil || s.redis == nil || tokenID == "" {
		return false, nil
	}

	key := fmt.Sprintf(tokenBlacklistPrefix, tokenID)
	exists, err := s.redis.Exists(ctx, key)
	if err != nil {
		return false, fmt.Errorf("failed to check blacklisted token: %w", err)
	}
	return exists, nil
}
