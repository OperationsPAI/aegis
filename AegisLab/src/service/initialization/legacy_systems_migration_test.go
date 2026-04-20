package initialization

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"aegis/consts"
	etcd "aegis/infra/etcd"
	"aegis/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// fakeEtcdClient implements etcdMigrationClient in-process so migration tests
// can exercise the copy + delete flow without a live etcd.
type fakeEtcdClient struct {
	data map[string]string
}

func newFakeEtcdClient() *fakeEtcdClient {
	return &fakeEtcdClient{data: map[string]string{}}
}

func (f *fakeEtcdClient) ListPrefix(_ context.Context, prefix string) ([]etcd.KeyValue, error) {
	keys := make([]string, 0)
	for k := range f.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]etcd.KeyValue, 0, len(keys))
	for _, k := range keys {
		out = append(out, etcd.KeyValue{Key: k, Value: f.data[k]})
	}
	return out, nil
}

func (f *fakeEtcdClient) Get(_ context.Context, key string) (string, error) {
	v, ok := f.data[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (f *fakeEtcdClient) Put(_ context.Context, key, value string, _ time.Duration) error {
	f.data[key] = value
	return nil
}

func (f *fakeEtcdClient) Delete(_ context.Context, key string) error {
	delete(f.data, key)
	return nil
}

func newMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&model.DynamicConfig{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestRescopeDynamicConfigsToGlobal flips Consumer-scoped injection.system.*
// rows to Global and leaves unrelated rows alone. Second run is a no-op.
func TestRescopeDynamicConfigsToGlobal(t *testing.T) {
	db := newMigrationDB(t)

	tsCount := &model.DynamicConfig{
		Key:          "injection.system.ts.count",
		DefaultValue: "5",
		ValueType:    consts.ConfigValueTypeInt,
		Scope:        consts.ConfigScopeConsumer,
		Category:     "injection.system.count",
	}
	otelStatus := &model.DynamicConfig{
		Key:          "injection.system.otel-demo.status",
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
		Scope:        consts.ConfigScopeConsumer,
		Category:     "injection.system.status",
	}
	unrelated := &model.DynamicConfig{
		Key:          "rate_limiting.max_concurrent_builds",
		DefaultValue: "3",
		ValueType:    consts.ConfigValueTypeInt,
		Scope:        consts.ConfigScopeConsumer,
		Category:     "rate_limiting",
	}
	alreadyGlobal := &model.DynamicConfig{
		Key:          "injection.system.ts.status",
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
		Scope:        consts.ConfigScopeGlobal,
		Category:     "injection.system.status",
	}
	for _, row := range []*model.DynamicConfig{tsCount, otelStatus, unrelated, alreadyGlobal} {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("seed row %s: %v", row.Key, err)
		}
	}

	if err := rescopeDynamicConfigsToGlobal(db); err != nil {
		t.Fatalf("rescope: %v", err)
	}

	// Verify injection.system.* rows are Global, unrelated row is untouched.
	check := func(key string, want consts.ConfigScope) {
		var got model.DynamicConfig
		if err := db.Where("config_key = ?", key).First(&got).Error; err != nil {
			t.Fatalf("lookup %s: %v", key, err)
		}
		if got.Scope != want {
			t.Fatalf("%s scope = %v, want %v", key, got.Scope, want)
		}
	}
	check("injection.system.ts.count", consts.ConfigScopeGlobal)
	check("injection.system.otel-demo.status", consts.ConfigScopeGlobal)
	check("injection.system.ts.status", consts.ConfigScopeGlobal)
	check("rate_limiting.max_concurrent_builds", consts.ConfigScopeConsumer)

	// Idempotence: second run is a no-op.
	if err := rescopeDynamicConfigsToGlobal(db); err != nil {
		t.Fatalf("second rescope: %v", err)
	}
	check("injection.system.ts.count", consts.ConfigScopeGlobal)
	check("rate_limiting.max_concurrent_builds", consts.ConfigScopeConsumer)
}

// TestDrainConsumerEtcdIntoGlobal moves Consumer-prefix injection.system.*
// keys to Global and deletes the Consumer copies. Running the drain a second
// time is a no-op (no keys under Consumer prefix to move).
func TestDrainConsumerEtcdIntoGlobal(t *testing.T) {
	fe := newFakeEtcdClient()

	// Seed: one stale Consumer copy, no existing Global copy.
	fe.data[consts.ConfigEtcdConsumerPrefix+"injection.system.ts.count"] = "5"
	// Another Consumer copy where the Global side already exists (should
	// win over the Consumer copy).
	fe.data[consts.ConfigEtcdConsumerPrefix+"injection.system.ts.status"] = "0"
	fe.data[consts.ConfigEtcdGlobalPrefix+"injection.system.ts.status"] = "1"
	// An unrelated Consumer key that must not be touched.
	fe.data[consts.ConfigEtcdConsumerPrefix+"rate_limiting.max_concurrent_builds"] = "3"

	if err := drainConsumerEtcdIntoGlobal(context.Background(), fe); err != nil {
		t.Fatalf("drain: %v", err)
	}

	// injection.system.ts.count: moved to Global, deleted from Consumer.
	if got := fe.data[consts.ConfigEtcdGlobalPrefix+"injection.system.ts.count"]; got != "5" {
		t.Fatalf("Global ts.count = %q, want 5", got)
	}
	if _, ok := fe.data[consts.ConfigEtcdConsumerPrefix+"injection.system.ts.count"]; ok {
		t.Fatalf("Consumer ts.count still present")
	}

	// injection.system.ts.status: Global already had "1" (winner), Consumer
	// copy should be deleted, Global value untouched.
	if got := fe.data[consts.ConfigEtcdGlobalPrefix+"injection.system.ts.status"]; got != "1" {
		t.Fatalf("Global ts.status = %q, want 1 (untouched)", got)
	}
	if _, ok := fe.data[consts.ConfigEtcdConsumerPrefix+"injection.system.ts.status"]; ok {
		t.Fatalf("Consumer ts.status still present")
	}

	// Unrelated Consumer key is untouched.
	if got := fe.data[consts.ConfigEtcdConsumerPrefix+"rate_limiting.max_concurrent_builds"]; got != "3" {
		t.Fatalf("unrelated Consumer key = %q, want 3 (untouched)", got)
	}

	// Idempotence: second run finds nothing under the Consumer injection.system
	// prefix and is a no-op.
	before := snapshot(fe.data)
	if err := drainConsumerEtcdIntoGlobal(context.Background(), fe); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	after := snapshot(fe.data)
	if !equalMaps(before, after) {
		t.Fatalf("second drain mutated state:\nbefore=%v\nafter=%v", before, after)
	}
}

// TestMigrateLegacyInjectionSystemNoOpWithoutTable ensures a fresh install
// (no systems table, empty dynamic_configs, empty etcd) completes without
// error and leaves every store empty.
func TestMigrateLegacyInjectionSystemNoOpWithoutTable(t *testing.T) {
	db := newMigrationDB(t)

	if err := MigrateLegacyInjectionSystem(context.Background(), db, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var count int64
	if err := db.Model(&model.DynamicConfig{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected empty dynamic_configs, got %d rows", count)
	}
}

func snapshot(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
