package common

import (
	"reflect"
	"sort"
	"testing"

	"aegis/model"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newMetadataStoreDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&model.SystemMetadata{}); err != nil {
		t.Fatalf("migrate system metadata: %v", err)
	}
	return db
}

func TestDBMetadataStoreTopologyFallbacks(t *testing.T) {
	db := newMetadataStoreDB(t)
	store := NewDBMetadataStore(db)

	rows := []model.SystemMetadata{
		{
			SystemName:   "bench-http",
			MetadataType: "service_topology",
			ServiceName:  "frontend",
			Data:         `{"name":"frontend","namespace":"bench-http0","pods":["frontend-0"],"depends_on":["checkout","adservice"]}`,
		},
		{
			SystemName:   "bench-http",
			MetadataType: "topology",
			ServiceName:  "checkout",
			Data:         `{"name":"checkout","depends_on":["payment"]}`,
		},
		{
			SystemName:   "bench-http",
			MetadataType: "runtime_mutator_target",
			ServiceName:  "checkout",
			Data:         `[{"AppName":"checkout","ClassName":"CartService","MethodName":"GetCart","MutationType":1,"MutationTypeName":"latency","MutationFrom":"","MutationTo":"","MutationStrategy":"replace","Description":"test"}]`,
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("insert metadata rows: %v", err)
	}

	names, err := store.GetAllServiceNames("bench-http")
	if err != nil {
		t.Fatalf("GetAllServiceNames() error = %v", err)
	}
	sort.Strings(names)
	if want := []string{"checkout", "frontend"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("GetAllServiceNames() = %v, want %v", names, want)
	}

	pairs, err := store.GetNetworkPairs("bench-http")
	if err != nil {
		t.Fatalf("GetNetworkPairs() error = %v", err)
	}
	gotPairs := make(map[string]struct{}, len(pairs))
	for _, pair := range pairs {
		gotPairs[pair.Source+"->"+pair.Target] = struct{}{}
	}
	for _, want := range []string{"frontend->checkout", "frontend->adservice", "checkout->payment"} {
		if _, ok := gotPairs[want]; !ok {
			t.Fatalf("expected derived network pair %s, got %v", want, gotPairs)
		}
	}

	targets, err := store.GetRuntimeMutatorTargets("bench-http")
	if err != nil {
		t.Fatalf("GetRuntimeMutatorTargets() error = %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("GetRuntimeMutatorTargets() len = %d, want 1", len(targets))
	}
	if targets[0] != (chaos.RuntimeMutatorTargetData{
		AppName:          "checkout",
		ClassName:        "CartService",
		MethodName:       "GetCart",
		MutationType:     1,
		MutationTypeName: "latency",
		MutationFrom:     "",
		MutationTo:       "",
		MutationStrategy: "replace",
		Description:      "test",
	}) {
		t.Fatalf("unexpected mutator target: %+v", targets[0])
	}
}
