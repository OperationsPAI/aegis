package chaossystem

import (
	"context"
	"testing"

	"aegis/config"
	"aegis/model"
	"aegis/service/common"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newChaosSystemService(t *testing.T) (*Service, *gorm.DB, *common.DBMetadataStore) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&model.System{}, &model.SystemMetadata{}); err != nil {
		t.Fatalf("migrate system tables: %v", err)
	}

	config.SetChaosConfigDB(db)
	store := common.NewDBMetadataStore(db)
	chaos.SetMetadataStore(store)

	return NewService(NewRepository(db)), db, store
}

func TestCreateSystemAndTopologyMetadataAreAvailableImmediately(t *testing.T) {
	service, _, store := newChaosSystemService(t)
	ctx := context.Background()

	systemName := "bench-http-runtime"
	if chaos.IsSystemRegistered(systemName) {
		_ = chaos.UnregisterSystem(systemName)
	}
	defer func() {
		if chaos.IsSystemRegistered(systemName) {
			_ = chaos.UnregisterSystem(systemName)
		}
	}()

	created, err := service.CreateSystem(ctx, &CreateChaosSystemReq{
		Name:           systemName,
		DisplayName:    "Bench HTTP Runtime",
		NsPattern:      "^bench-http-runtime\\d+$",
		ExtractPattern: "^(bench-http-runtime)(\\d+)$",
		AppLabelKey:    "app.kubernetes.io/name",
		Count:          1,
	})
	if err != nil {
		t.Fatalf("CreateSystem() error = %v", err)
	}
	if created.AppLabelKey != "app.kubernetes.io/name" {
		t.Fatalf("CreateSystem() app_label_key = %q, want %q", created.AppLabelKey, "app.kubernetes.io/name")
	}
	if !chaos.IsSystemRegistered(systemName) {
		t.Fatalf("system %s was not registered in chaos-experiment", systemName)
	}

	cfg, ok := config.GetChaosSystemConfigManager().Get(chaos.SystemType(systemName))
	if !ok {
		t.Fatalf("chaos system config manager did not load %s", systemName)
	}
	if cfg.NsPattern != "^bench-http-runtime\\d+$" {
		t.Fatalf("config manager ns_pattern = %q", cfg.NsPattern)
	}

	// Prime the cache with an empty lookup, then ensure UpsertMetadata invalidates it.
	names, err := store.GetAllServiceNames(systemName)
	if err != nil {
		t.Fatalf("initial GetAllServiceNames() error = %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("initial GetAllServiceNames() = %v, want empty", names)
	}

	err = service.UpsertMetadata(ctx, created.ID, &BulkUpsertSystemMetadataReq{
		Services: []TopologyServiceReq{
			{Name: "frontend", Namespace: "bench-http-runtime0", DependsOn: []string{"checkout"}},
			{Name: "checkout", Namespace: "bench-http-runtime0"},
		},
	})
	if err != nil {
		t.Fatalf("UpsertMetadata() error = %v", err)
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
