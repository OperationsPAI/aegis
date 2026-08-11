package state

import (
	"context"
	"fmt"
	"strconv"

	"aegis/platform/consts"
	redisinfra "aegis/platform/redis"

	goredis "github.com/redis/go-redis/v9"
)

type StatusStore struct {
	client *redisinfra.Gateway
}

func NewStatusStore(client *redisinfra.Gateway) StatusStore {
	return StatusStore{client: client}
}

func (s StatusStore) Key(namespace string) string {
	return fmt.Sprintf(consts.NamespaceKeyPattern, namespace)
}

func (s StatusStore) Get(ctx context.Context, namespace string) (consts.StatusType, error) {
	statusStr, err := s.client.HashGet(ctx, s.Key(namespace), "status")
	if err == goredis.Nil {
		return consts.CommonEnabled, nil
	}
	if err != nil {
		return 0, err
	}

	status, err := strconv.Atoi(statusStr)
	if err != nil {
		return 0, fmt.Errorf("invalid status value: %w", err)
	}
	return consts.StatusType(status), nil
}

func (s StatusStore) Set(ctx context.Context, namespace string, status consts.StatusType) error {
	return s.client.HashSet(ctx, s.Key(namespace), map[string]any{"status": int(status)})
}
