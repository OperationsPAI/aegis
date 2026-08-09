package chaossystem

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// TestEtcd is the minimal etcd surface that integration tests across
// package boundaries need to exercise OnboardSystem / ExportSeed without
// standing up a real etcd cluster. Production callers go through the
// concrete *etcd.Gateway via NewService.
type TestEtcd interface {
	Get(ctx context.Context, key string) (string, error)
	Put(ctx context.Context, key, value string, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// NewServiceWithEtcd is a non-default constructor used by cross-package
// tests (e.g. cli/cmd round-trip) that need a Service bound to a sqlite
// gorm.DB + an in-memory etcd double. Not used by production wiring — fx
// continues to call NewService(*Repository, *etcd.Gateway).
func NewServiceWithEtcd(db *gorm.DB, etcd TestEtcd) *Service {
	return &Service{repo: NewRepository(db), etcd: etcd}
}
