package chaossystem

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/model"

	chaos "aegis/platform/chaos"
	"github.com/spf13/viper"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeEtcd struct {
	data map[string]string
}

func (f *fakeEtcd) Get(_ context.Context, key string) (string, error) {
	if value, ok := f.data[key]; ok {
		return value, nil
	}
	return "", fmt.Errorf("key not found: %s", key)
}

func (f *fakeEtcd) Put(_ context.Context, key, value string, _ time.Duration) error {
	f.data[key] = value
	return nil
}

func (f *fakeEtcd) Delete(_ context.Context, key string) error {
	delete(f.data, key)
	return nil
}

// newMetadataService spins up an in-memory service stripped of the etcd
// gateway. Sufficient for exercising the metadata upsert / namespace-count
// flow; the etcd-backed CRUD is covered by service_registry_test in the
// consumer package, which is the canonical contract for issue #75.
//
// The previous variant also returned a DBMetadataStore (used by a sibling
// cache-invalidation test). That store has moved into chaos-service along
// with its cache; the cross-process invalidation hook is gone, so the
// fixture stays minimal.
func newMetadataService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&model.DynamicConfig{}, &model.ConfigHistory{}, &model.SystemMetadata{}); err != nil {
		t.Fatalf("migrate tables: %v", err)
	}

	svc := &Service{
		repo: NewRepository(db),
		etcd: &fakeEtcd{data: map[string]string{}},
	}
	return svc, db
}

// seedSystemInViper installs a chaos-system entry into the global Viper tree
// so lookupByID/lookupByName can find it. Returns a cleanup closure.
func seedSystemInViper(t *testing.T, name string, isBuiltin bool) func() {
	t.Helper()
	key := "injection.system." + name
	prev := viper.Get("injection.system")
	viper.Set(key, map[string]any{
		"count":           1,
		"ns_pattern":      "^" + name + `\d+$`,
		"extract_pattern": "^(" + name + `)(\d+)$`,
		"display_name":    name,
		"app_label_key":   "app",
		"is_builtin":      isBuiltin,
		"status":          int(consts.CommonEnabled),
	})
	return func() { viper.Set("injection.system", prev) }
}

func TestUpdateSystemStatusRejectsDeleteSentinel(t *testing.T) {
	service, db := newMetadataService(t)
	const systemName = "bench-update-status-delete"
	cleanup := seedSystemInViper(t, systemName, false)
	defer cleanup()

	anchor := &model.DynamicConfig{
		Key:          systemKey(systemName, fieldCount),
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
	}
	if err := db.Create(anchor).Error; err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	deleted := int(consts.CommonDeleted)
	_, err := service.UpdateSystem(context.Background(), anchor.ID, &UpdateChaosSystemReq{Status: &deleted})
	if err == nil {
		t.Fatal("UpdateSystem: expected error for status=-1, got nil")
	}
	if !errors.Is(err, consts.ErrBadRequest) {
		t.Errorf("UpdateSystem: error should wrap ErrBadRequest; got %v", err)
	}
}

func TestEnsureCountForNamespaceNoBumpWhenInRange(t *testing.T) {
	service, db := newMetadataService(t)
	const systemName = "bench-count-noop"
	cleanup := seedSystemInViper(t, systemName, false)
	defer cleanup()
	viper.Set("injection.system."+systemName+".count", 5)

	anchor := &model.DynamicConfig{
		Key:          systemKey(systemName, fieldCount),
		DefaultValue: "5",
		ValueType:    consts.ConfigValueTypeInt,
	}
	if err := db.Create(anchor).Error; err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	bumped, err := service.EnsureCountForNamespace(context.Background(), systemName, systemName+"3")
	if err != nil {
		t.Fatalf("EnsureCountForNamespace: %v", err)
	}
	if bumped {
		t.Fatalf("EnsureCountForNamespace: expected no bump for in-range ns, got bumped=true")
	}
}

func TestEnsureCountForNamespaceRejectsMismatch(t *testing.T) {
	service, db := newMetadataService(t)
	const systemName = "bench-count-mismatch"
	cleanup := seedSystemInViper(t, systemName, false)
	defer cleanup()

	anchor := &model.DynamicConfig{
		Key:          systemKey(systemName, fieldCount),
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
	}
	if err := db.Create(anchor).Error; err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	_, err := service.EnsureCountForNamespace(context.Background(), systemName, "totally-unrelated-ns")
	if err == nil {
		t.Fatal("EnsureCountForNamespace: expected mismatch error, got nil")
	}
	if !errors.Is(err, consts.ErrBadRequest) {
		t.Errorf("EnsureCountForNamespace: error should wrap ErrBadRequest; got %v", err)
	}
}

func TestEnsureCountForNamespaceKeepsBootstrapIndexMoving(t *testing.T) {
	service, db := newMetadataService(t)
	const systemName = "sockshop"
	cleanup := seedSystemInViper(t, systemName, false)
	defer cleanup()

	anchor := &model.DynamicConfig{
		Key:          systemKey(systemName, fieldCount),
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
		Scope:        consts.ConfigScopeGlobal,
	}
	if err := db.Create(anchor).Error; err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	etcdKey := consts.ConfigEtcdGlobalPrefix + anchor.Key
	service.etcd.(*fakeEtcd).data[etcdKey] = "1"

	got := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		cfg, ok := config.GetChaosSystemConfigManager().Get(chaos.SystemType(systemName))
		if !ok {
			t.Fatalf("system %s not found in config manager", systemName)
		}
		ns := fmt.Sprintf("%s%d", systemName, cfg.Count)
		got = append(got, ns)

		bumped, err := service.EnsureCountForNamespace(context.Background(), systemName, ns)
		if err != nil {
			t.Fatalf("EnsureCountForNamespace(%s): %v", ns, err)
		}
		if !bumped {
			t.Fatalf("EnsureCountForNamespace(%s): expected bump", ns)
		}
	}

	want := []string{"sockshop1", "sockshop2", "sockshop3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bootstrap sequence = %v, want %v", got, want)
		}
	}

	cfg, ok := config.GetChaosSystemConfigManager().Get(chaos.SystemType(systemName))
	if !ok {
		t.Fatalf("system %s missing after bumps", systemName)
	}
	if cfg.Count != 4 {
		t.Fatalf("final count = %d, want 4", cfg.Count)
	}
	if service.etcd.(*fakeEtcd).data[etcdKey] != "4" {
		t.Fatalf("etcd count = %s, want 4", service.etcd.(*fakeEtcd).data[etcdKey])
	}
}

func TestUpdateSystemStatusRejectsBuiltin(t *testing.T) {
	service, db := newMetadataService(t)
	const systemName = "bench-update-status-builtin"
	cleanup := seedSystemInViper(t, systemName, true)
	defer cleanup()

	anchor := &model.DynamicConfig{
		Key:          systemKey(systemName, fieldCount),
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
	}
	if err := db.Create(anchor).Error; err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	disabled := int(consts.CommonDisabled)
	_, err := service.UpdateSystem(context.Background(), anchor.ID, &UpdateChaosSystemReq{Status: &disabled})
	if err == nil {
		t.Fatal("UpdateSystem: expected error disabling a builtin, got nil")
	}
	if !errors.Is(err, consts.ErrBadRequest) {
		t.Errorf("UpdateSystem: error should wrap ErrBadRequest; got %v", err)
	}
}
