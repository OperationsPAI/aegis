package state

import (
	"context"
	"fmt"
	"time"

	"aegis/platform/consts"
	redis "aegis/platform/redis"
)

type CatalogStore struct {
	client *redis.Gateway
}

func NewCatalogStore(client *redis.Gateway) CatalogStore {
	return CatalogStore{client: client}
}

func (s CatalogStore) Key(namespace string) string {
	return fmt.Sprintf(consts.NamespaceKeyPattern, namespace)
}

func (s CatalogStore) List(ctx context.Context) ([]string, error) {
	return s.client.SetMembers(ctx, consts.NamespacesKey)
}

func (s CatalogStore) Exists(ctx context.Context, namespace string) (bool, error) {
	return s.client.Exists(ctx, s.Key(namespace))
}

func (s CatalogStore) Seed(ctx context.Context, namespace string, endTime time.Time) error {
	return s.client.SeedNamespaceState(ctx, s.Key(namespace), namespace, endTime.Unix(), int(consts.CommonEnabled))
}
