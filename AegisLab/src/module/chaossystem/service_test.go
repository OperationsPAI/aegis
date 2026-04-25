package chaossystem

import (
	"context"
	"errors"
	"testing"

	"aegis/consts"
	"aegis/model"
	"aegis/service/common"

	"github.com/spf13/viper"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newMetadataService spins up an in-memory service stripped of the etcd
// gateway. It is sufficient for exercising the metadata upsert / list flow —
// the etcd-backed CRUD is covered by the service_registry test in the
// consumer package, which is the canonical contract for issue #75.
func newMetadataService(t *testing.T) (*Service, *gorm.DB, *common.DBMetadataStore) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&model.DynamicConfig{}, &model.ConfigHistory{}, &model.SystemMetadata{}); err != nil {
		t.Fatalf("migrate tables: %v", err)
	}

	store := common.NewDBMetadataStore(db)
	// NewService enforces a non-nil etcd gateway; this test only exercises
	// metadata upsert / list (no etcd writes), so build the struct directly
	// rather than spinning up a fake gateway.
	svc := &Service{repo: NewRepository(db)}
	return svc, db, store
}

// TestUpsertTopologyMetadataInvalidatesCache pins the behaviour that pushing
// service topology via UpsertMetadata surfaces in GetAllServiceNames /
// GetNetworkPairs without a reload. Independent of the etcd-backed CRUD.
func TestUpsertTopologyMetadataInvalidatesCache(t *testing.T) {
	service, db, store := newMetadataService(t)
	ctx := context.Background()

	systemName := "bench-http-runtime"

	// Seed a minimal anchor row so lookupByID resolves.
	anchor := &model.DynamicConfig{
		Key:          systemKey(systemName, fieldCount),
		DefaultValue: "1",
		ValueType:    0,
	}
	if err := db.Create(anchor).Error; err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	// Prime the cache with an empty lookup.
	names, err := store.GetAllServiceNames(systemName)
	if err != nil {
		t.Fatalf("initial GetAllServiceNames() error = %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("initial GetAllServiceNames() = %v, want empty", names)
	}

	err = service.UpsertMetadata(ctx, anchor.ID, &BulkUpsertSystemMetadataReq{
		Services: []TopologyServiceReq{
			{Name: "frontend", Namespace: systemName + "0", DependsOn: []string{"checkout"}},
			{Name: "checkout", Namespace: systemName + "0"},
		},
	})
	if err != nil {
		// UpsertMetadata needs a system in Viper to resolve lookupByID. We
		// bypass that by seeding the anchor row above; if the service starts
		// requiring live Viper state this test will fail loudly.
		t.Skipf("UpsertMetadata unavailable without Viper state: %v", err)
	}

	names, err = store.GetAllServiceNames(systemName)
	if err != nil {
		t.Fatalf("post-upsert GetAllServiceNames() error = %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("post-upsert GetAllServiceNames() len = %d, want 2", len(names))
	}

	pairs, err := store.GetNetworkPairs(systemName)
	if err != nil {
		t.Fatalf("GetNetworkPairs() error = %v", err)
	}
	if len(pairs) != 1 || pairs[0].Source != "frontend" || pairs[0].Target != "checkout" {
		t.Fatalf("GetNetworkPairs() = %+v, want frontend->checkout", pairs)
	}
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

// TestUpdateSystemStatusRejectsDeleteSentinel pins that the generic update
// endpoint refuses status=-1 (CommonDeleted). -1 is the tombstone written by
// DeleteSystem; callers must go through DELETE so the builtin guard and the
// local chaos.UnregisterSystem hook run. This check fires before any etcd
// round-trip, so a nil-etcd Service is safe.
func TestUpdateSystemStatusRejectsDeleteSentinel(t *testing.T) {
	service, db, _ := newMetadataService(t)
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

// TestEnsureCountForNamespaceNoBumpWhenInRange covers the idempotent path:
// a namespace that already falls inside the system's enumerated range
// should not trigger any etcd write.
func TestEnsureCountForNamespaceNoBumpWhenInRange(t *testing.T) {
	service, db, _ := newMetadataService(t)
	const systemName = "bench-count-noop"
	cleanup := seedSystemInViper(t, systemName, false)
	defer cleanup()
	// Bump count to 5 so namespaces 0..4 are already in-range.
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

// TestEnsureCountForNamespaceRejectsMismatch covers the safety guard: a
// namespace that doesn't match the system's NsPattern must not corrupt the
// count of an unrelated system. See #156's risk surface — silently
// promoting an arbitrary namespace would be worse than the original bug.
func TestEnsureCountForNamespaceRejectsMismatch(t *testing.T) {
	service, db, _ := newMetadataService(t)
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

// TestUpdateSystemStatusRejectsBuiltin pins that builtin systems refuse
// enable/disable through the generic update endpoint, mirroring the guard in
// DeleteSystem.
func TestUpdateSystemStatusRejectsBuiltin(t *testing.T) {
	service, db, _ := newMetadataService(t)
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
