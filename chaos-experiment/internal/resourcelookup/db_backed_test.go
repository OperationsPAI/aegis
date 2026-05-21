package resourcelookup

import (
	"context"
	"sort"
	"testing"

	"github.com/OperationsPAI/chaos-experiment/internal/systemconfig"
)

// stubStore returns canned chaos_points rows; mirrors the shape aegislab
// produces from PointManifest imports without dragging gorm into this
// package's test compile path.
type stubStore struct {
	rows map[string][]ChaosPointRow
}

func (s *stubStore) QueryPoints(_ context.Context, system string) ([]ChaosPointRow, error) {
	return s.rows[system], nil
}

func newStubStore() *stubStore {
	return &stubStore{rows: map[string][]ChaosPointRow{
		"ts": {
			// HTTP family — 3 capabilities on the same (app, path) collapse to 1 endpoint.
			{SystemName: "ts", CapabilityName: "http_response_abort", Target: map[string]any{
				"app": "ts-user-service", "method": "GET", "path": "/api/users", "port": float64(8080),
			}},
			{SystemName: "ts", CapabilityName: "http_response_delay", Target: map[string]any{
				"app": "ts-user-service", "method": "GET", "path": "/api/users", "port": float64(8080),
			}},
			{SystemName: "ts", CapabilityName: "http_request_replace_path", Target: map[string]any{
				"app": "ts-order-service", "method": "POST", "path": "/api/orders", "port": float64(8080),
			}},
			// Network
			{SystemName: "ts", CapabilityName: "network_delay", Target: map[string]any{
				"source_app": "ts-user-service", "target_service": "ts-auth-service",
			}},
			{SystemName: "ts", CapabilityName: "network_loss", Target: map[string]any{
				"source_app": "ts-user-service", "target_service": "ts-auth-service",
			}},
			// DNS — domain_patterns is an array
			{SystemName: "ts", CapabilityName: "dns_error", Target: map[string]any{
				"app": "ts-user-service", "domain_patterns": []any{"ts-auth-service", "ts-config-service"},
			}},
			// JVM method
			{SystemName: "ts", CapabilityName: "jvm_method_exception", Target: map[string]any{
				"app": "ts-user-service", "class": "user.UserService", "method": "findById",
			}},
			// JVM mysql → database operations family
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
// the prior state on cleanup. Avoids leaking state across tests in the
// same package — registry_test / lookup_test rely on the static path.
func withChaosStore(t *testing.T, s ChaosPointStore) {
	t.Helper()
	prev := getChaosPointStore()
	SetChaosPointStore(s)
	t.Cleanup(func() { SetChaosPointStore(prev) })
	// Drop any cached results from a previous configuration.
	ResetSystemCache(systemconfig.SystemTrainTicket)
	ResetSystemCache(systemconfig.SystemOtelDemo)
}

func TestDBBacked_HTTPEndpoints_CollapsesCapabilities(t *testing.T) {
	withChaosStore(t, newStubStore())

	got, err := GetSystemCache(systemconfig.SystemTrainTicket).GetAllHTTPEndpoints()
	if err != nil {
		t.Fatalf("GetAllHTTPEndpoints: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 endpoints (two capabilities collapse), got %d: %#v", len(got), got)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].AppName < got[j].AppName })
	if got[0].AppName != "ts-order-service" || got[0].Route != "/api/orders" {
		t.Errorf("first endpoint wrong: %#v", got[0])
	}
	if got[1].AppName != "ts-user-service" || got[1].Route != "/api/users" || got[1].ServerPort != "8080" {
		t.Errorf("second endpoint wrong: %#v", got[1])
	}
}

func TestDBBacked_NetworkPairs_Dedup(t *testing.T) {
	withChaosStore(t, newStubStore())

	got, err := GetSystemCache(systemconfig.SystemTrainTicket).GetAllNetworkPairs()
	if err != nil {
		t.Fatalf("GetAllNetworkPairs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 pair (network_delay + network_loss collapse), got %d: %#v", len(got), got)
	}
	if got[0].SourceService != "ts-user-service" || got[0].TargetService != "ts-auth-service" {
		t.Errorf("pair wrong: %#v", got[0])
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

	// Otel-demo cache must not see ts's points.
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

func TestNewSystemCache_InstallsStore(t *testing.T) {
	prev := getChaosPointStore()
	t.Cleanup(func() {
		SetChaosPointStore(prev)
		ResetSystemCache(systemconfig.SystemTrainTicket)
	})
	ResetSystemCache(systemconfig.SystemTrainTicket)

	s := newStubStore()
	cache := NewSystemCache(s, systemconfig.SystemTrainTicket)
	got, err := cache.GetAllNetworkPairs()
	if err != nil {
		t.Fatalf("GetAllNetworkPairs: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected store to be active, got %d pairs", len(got))
	}
}
