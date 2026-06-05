package auth

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const revocationKeyPrefix = "auth:revoked:"

type RedisRevocationStore struct {
	client redis.Cmdable
}

func NewRedisRevocationStore(client redis.Cmdable) *RedisRevocationStore {
	return &RedisRevocationStore{client: client}
}

func (s *RedisRevocationStore) Revoke(ctx context.Context, jti string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return s.client.Set(ctx, revocationKeyPrefix+jti, "1", ttl).Err()
}

func (s *RedisRevocationStore) IsRevoked(ctx context.Context, jti string) (bool, error) {
	n, err := s.client.Exists(ctx, revocationKeyPrefix+jti).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
