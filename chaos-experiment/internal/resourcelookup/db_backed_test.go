package resourcelookup

import (
	"context"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/OperationsPAI/chaos-experiment/internal/systemconfig"
)

// stubStore returns canned chaos_points rows; mirrors the shape aegislab
// produces from PointManifest imports without dragging gorm into this
// package's test compile path. queryCount lets shared-snapshot tests
// assert the warm-up does one DB hit per (system, cache).
type stubStore struct {
	rows       map[string][]ChaosPointRow
	queryCount int64
}

func (s *stubStore) QueryPoints(_ context.Context, system string) ([]ChaosPointRow, error) {
	atomic.AddInt64(&s.queryCount, 1)
	return s.rows[system], nil
}

func newStubStore() *stubStore {
	return &stubStore{rows: map[string][]ChaosPointRow{
		"ts": {
			// HTTP family — 3 capabilities on the same (app, path) collapse to 1
			// endpoint. The first row carries server_address + span_name (dump
			// tool output); the others omit them, exercising the "metadata-only
			// fields are preserved across the collapse" path.
			{SystemName: "ts", CapabilityName: "http_response_abort", Target: map[string]any{
				"app": "ts-user-service", "method": "GET", "path": "/api/users", "port": float64(8080),
				"server_address": "ts-auth-service",
				"span_name":      "GET /api/users",
			}},
			{SystemName: "ts", CapabilityName: "http_response_delay", Target: map[string]any{
				"app": "ts-user-service", "method": "GET", "path": "/api/users", "port": float64(8080),
				"server_address": "ts-auth-service",
				"span_name":      "GET /api/users",
			}},
			// Chart-author-style HTTP point: required fields only, no extras.
			{SystemName: "ts", CapabilityName: "http_request_replace_path", Target: map[string]any{
				"app": "ts-order-service", "method": "POST", "path": "/api/orders", "port": float64(8080),
			}},
			// Network — span_names carried alongside required fields.
			{SystemName: "ts", CapabilityName: "network_delay", Target: map[string]any{
				"source_app": "ts-user-service", "target_service": "ts-auth-service",
				"span_names": []any{"GET /authenticate", "POST /token"},
			}},
			{SystemName: "ts", CapabilityName: "network_loss", Target: map[string]any{
				"source_app": "ts-user-service", "target_service": "ts-auth-service",
				"span_names": []any{"GET /authenticate", "POST /token"},
			}},
			// Chart-author-style network point: no span_names.
			{SystemName: "ts", CapabilityName: "network_partition", Target: map[string]any{
				"source_app": "ts-config-service", "target_service": "ts-registry",
			}},
			// DNS — domain_patterns is an array.
			{SystemName: "ts", CapabilityName: "dns_error", Target: map[string]any{
				"app": "ts-user-service", "domain_patterns": []any{"ts-auth-service", "ts-config-service"},
			}},
			// JVM method.
			{SystemName: "ts", CapabilityName: "jvm_method_exception", Target: map[string]any{
				"app": "ts-user-service", "class": "user.UserService", "method": "findById",
			}},
			// JVM mysql → database operations family.
			{SystemName: "ts", CapabilityName: "jvm_mysql_latency", Target: map[string]any{
				"app": "ts-user-service", "db_name": "ts", "table": "users", "sql_type": "select",
			}},
		},
		"otel-demo": {
			{SystemName: "otel-demo", CapabilityName: "http_response_abort", Target: map[string]any{
				"app": "checkout", "method": "POST", "path": "/ship-order", "port": float64(8080),
			}},
		},
	}}
}

// withChaosStore installs the given store for the duration of t, restoring
// the prior state on cleanup.
func withChaosStore(t *testing.T, s ChaosPointStore) {
	t.Helper()
	prev := getChaosPointStore()
	SetChaosPointStore(s)
	t.Cleanup(func() { SetChaosPointStore(prev) })
	ResetSystemCache(systemconfig.SystemTrainTicket)
	ResetSystemCache(systemconfig.SystemOtelDemo)
}

func TestDBBacked_HTTPEndpoints_PreservesGroundtruthMetadata(t *testing.T) {
	withChaosStore(t, newStubStore())

	got, err := GetSystemCache(systemconfig.SystemTrainTicket).GetAllHTTPEndpoints()
	if err != nil {
		t.Fatalf("GetAllHTTPEndpoints: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 endpoints (collapse on app|method|path|port), got %d: %#v", len(got), got)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].AppName < got[j].AppName })

	// Chart-author entry: required fields only; server_address / span_name empty.
	if got[0].AppName != "ts-order-service" || got[0].Route != "/api/orders" {
		t.Errorf("first endpoint wrong: %#v", got[0])
	}
	if got[0].ServerAddress != "" || got[0].SpanName != "" {
		t.Errorf("chart-author entry should expose empty metadata, got server_address=%q span_name=%q",
			got[0].ServerAddress, got[0].SpanName)
	}

	// Dump-tool entry: metadata fields populated.
	if got[1].AppName != "ts-user-service" || got[1].Route != "/api/users" || got[1].ServerPort != "8080" {
		t.Errorf("second endpoint wrong: %#v", got[1])
	}
	if got[1].ServerAddress != "ts-auth-service" {
		t.Errorf("want ServerAddress=ts-auth-service for dump-tool entry, got %q", got[1].ServerAddress)
	}
	if got[1].SpanName != "GET /api/users" {
		t.Errorf("want SpanName=GET /api/users for dump-tool entry, got %q", got[1].SpanName)
	}
}

func TestDBBacked_NetworkPairs_CarriesSpanNames(t *testing.T) {
	withChaosStore(t, newStubStore())

	got, err := GetSystemCache(systemconfig.SystemTrainTicket).GetAllNetworkPairs()
	if err != nil {
		t.Fatalf("GetAllNetworkPairs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 pairs, got %d: %#v", len(got), got)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].SourceService < got[j].SourceService })

	// Chart-author entry: no span_names.
	if got[0].SourceService != "ts-config-service" || got[0].TargetService != "ts-registry" {
		t.Errorf("first pair wrong: %#v", got[0])
	}
	if len(got[0].SpanNames) != 0 {
		t.Errorf("chart-author entry should expose empty SpanNames, got %v", got[0].SpanNames)
	}

	// Dump-tool entry: span_names populated; dedup across both rows.
	if got[1].SourceService != "ts-user-service" || got[1].TargetService != "ts-auth-service" {
		t.Errorf("second pair wrong: %#v", got[1])
	}
	if len(got[1].SpanNames) != 2 {
		t.Fatalf("want 2 SpanNames for dump-tool entry, got %d: %v", len(got[1].SpanNames), got[1].SpanNames)
	}
	wantSpans := map[string]bool{"GET /authenticate": true, "POST /token": true}
	for _, s := range got[1].SpanNames {
		if !wantSpans[s] {
			t.Errorf("unexpected span name %q", s)
		}
	}
}

func TestDBBacked_DNSEndpoints_ExpandsDomainPatterns(t *testing.T) {
	withChaosStore(t, newStubStore())

	got, err := GetSystemCache(systemconfig.SystemTrainTicket).GetAllDNSEndpoints()
	if err != nil {
		t.Fatalf("GetAllDNSEndpoints: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 dns endpoints (one per domain pattern), got %d", len(got))
	}
	domains := map[string]bool{got[0].Domain: true, got[1].Domain: true}
	if !domains["ts-auth-service"] || !domains["ts-config-service"] {
		t.Errorf("missing expected domain: %#v", got)
	}
}

func TestDBBacked_JVMMethods(t *testing.T) {
	withChaosStore(t, newStubStore())

	got, err := GetSystemCache(systemconfig.SystemTrainTicket).GetAllJVMMethods()
	if err != nil {
		t.Fatalf("GetAllJVMMethods: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 method, got %d", len(got))
	}
	if got[0].AppName != "ts-user-service" || got[0].ClassName != "user.UserService" || got[0].MethodName != "findById" {
		t.Errorf("method wrong: %#v", got[0])
	}
}

func TestDBBacked_DatabaseOperations_FromJVMMysqlFamily(t *testing.T) {
	withChaosStore(t, newStubStore())

	got, err := GetSystemCache(systemconfig.SystemTrainTicket).GetAllDatabaseOperations()
	if err != nil {
		t.Fatalf("GetAllDatabaseOperations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 db op, got %d", len(got))
	}
	if got[0].AppName != "ts-user-service" || got[0].DBName != "ts" || got[0].TableName != "users" || got[0].OperationType != "select" {
		t.Errorf("db op wrong: %#v", got[0])
	}
}

func TestDBBacked_PerSystemScope(t *testing.T) {
	withChaosStore(t, newStubStore())

	got, err := GetSystemCache(systemconfig.SystemOtelDemo).GetAllHTTPEndpoints()
	if err != nil {
		t.Fatalf("GetAllHTTPEndpoints: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 otel-demo endpoint, got %d", len(got))
	}
	if got[0].AppName != "checkout" {
		t.Errorf("otel-demo endpoint wrong: %#v", got[0])
	}
}

// TestDBBacked_SharedSnapshot_OneQueryPerWarmup proves the five DB-backed
// GetAllX methods reuse a single chaos_points snapshot per cache lifetime.
// Without this, a guided walk that consults all five families issues five
// MySQL round-trips on every warm-up.
func TestDBBacked_SharedSnapshot_OneQueryPerWarmup(t *testing.T) {
	store := newStubStore()
	withChaosStore(t, store)

	cache := GetSystemCache(systemconfig.SystemTrainTicket)
	if _, err := cache.GetAllHTTPEndpoints(); err != nil {
		t.Fatalf("http: %v", err)
	}
	if _, err := cache.GetAllNetworkPairs(); err != nil {
		t.Fatalf("network: %v", err)
	}
	if _, err := cache.GetAllDNSEndpoints(); err != nil {
		t.Fatalf("dns: %v", err)
	}
	if _, err := cache.GetAllJVMMethods(); err != nil {
		t.Fatalf("jvm: %v", err)
	}
	if _, err := cache.GetAllDatabaseOperations(); err != nil {
		t.Fatalf("db: %v", err)
	}

	if n := atomic.LoadInt64(&store.queryCount); n != 1 {
		t.Errorf("want exactly 1 QueryPoints call across 5 GetAllX, got %d", n)
	}
}
