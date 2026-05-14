package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"
	redis "aegis/platform/redis"
)

// tokenBlacklistPrefix keys every revoked token by its JWT ID (claims.ID).
// Blacklist additions set an EX TTL equal to the token's remaining lifetime,
// and lookups use EXISTS against this exact key — never SCAN MATCH —
// so the per-request auth cost stays O(1) regardless of blacklist size.
const tokenBlacklistPrefix = "blacklist:token:%s"
const apiKeyNoncePrefix = "api_key:nonce:%s:%s"

// userRevocationPrefix keys a per-user "revoke all tokens issued before this
// timestamp" marker. Set when an admin resets the user's password (or for any
// future force-logout-all). Verify path GETs this key and compares the stored
// unix timestamp against claims.iat to reject pre-revocation tokens. TTL is
// chosen to cover the longest token lifetime so the marker stays effective
// until every pre-revocation token has expired naturally.
const userRevocationPrefix = "revoke:user:%d"

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

// RevokeAllForUser invalidates every still-live access token issued to userID
// before now. TTL covers the longest token lifetime (refresh-token window)
// so the marker outlives any token that might have predated the call.
func (s *TokenStore) RevokeAllForUser(ctx context.Context, userID int) error {
	if s == nil || s.redis == nil {
		return nil
	}
	if userID <= 0 {
		return fmt.Errorf("invalid user id: %d", userID)
	}
	key := fmt.Sprintf(userRevocationPrefix, userID)
	now := strconv.FormatInt(time.Now().Unix(), 10)
	if err := s.redis.Set(ctx, key, now, crypto.RefreshTokenExpiration); err != nil {
		return fmt.Errorf("failed to mark user %d tokens revoked: %w", userID, err)
	}
	return nil
}

// UserRevokedSince returns the cutoff time after which tokens for userID
// remain valid. If no revocation has been recorded the second return value
// is false and the time is zero.
func (s *TokenStore) UserRevokedSince(ctx context.Context, userID int) (time.Time, bool, error) {
	if s == nil || s.redis == nil || userID <= 0 {
		return time.Time{}, false, nil
	}
	key := fmt.Sprintf(userRevocationPrefix, userID)
	raw, err := s.redis.GetString(ctx, key)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("failed to read user-revocation marker: %w", err)
	}
	if raw == "" {
		return time.Time{}, false, nil
	}
	ts, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("invalid user-revocation timestamp %q: %w", raw, err)
	}
	return time.Unix(ts, 0), true, nil
}
