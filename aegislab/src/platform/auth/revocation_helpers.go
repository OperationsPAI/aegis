package auth

import (
	"context"
	"time"
)

func RevokeToken(ctx context.Context, store RevocationStore, jti string, expiresAt time.Time) error {
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return store.Revoke(ctx, jti, ttl)
}
