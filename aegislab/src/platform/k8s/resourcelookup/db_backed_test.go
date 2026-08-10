package resourcelookup

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"aegis/platform/systemconfig"
)

// stubStore returns canned chaos_points rows; mirrors the shape aegislab
// produces from PointManifest imports without dragging gorm into this
// package's test compile path. queryCount lets shared-snapshot tests
// assert the warm-up does one DB hit per (system, cache); latestPerSystem +
// latestErr drive the LatestUpdate probe so invalidation tests can swing
// the high-water mark without touching MySQL.
type stubStore struct {
	mu                sync.Mutex
	rows              map[string][]ChaosPointRow
	latestPerSystem   map[string]time.Time
	latestErr         error
	queryCount        int64
	latestUpdateCount int64
}

func (s *stubStore) QueryPoints(_ context.Context, system string) ([]ChaosPointRow, error) {
	atomic.AddInt64(&s.queryCount, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[system], nil
}

func (s *stubStore) LatestUpdate(_ context.Context, system string) (time.Time, error) {
	atomic.AddInt64(&s.latestUpdateCount, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latestErr != nil {
		return time.Time{}, s.latestErr
	}
	return s.latestPerSystem[system], nil
}

func (s *stubStore) setLatest(system string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latestPerSystem == nil {
		s.latestPerSystem = map[string]time.Time{}
	}
	s.latestPerSystem[system] = t
}

func (s *stubStore) setRows(system string, rows []ChaosPointRow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[system] = rows
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
	ResetSystemCache(systemconfig.SystemType("ts"))
	ResetSystemCache(systemconfig.SystemType("otel-demo"))
}

func TestDBBacked_HTTPEndpoints_PreservesGroundtruthMetadata(t *testing.T) {
	withChaosStore(t, newStubStore())

	got, err := GetSystemCache(systemconfig.SystemType("ts")).GetAllHTTPEndpoints()
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

	got, err := GetSystemCache(systemconfig.SystemType("ts")).GetAllNetworkPairs()
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

	got, err := GetSystemCache(systemconfig.SystemType("ts")).GetAllDNSEndpoints()
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

	got, err := GetSystemCache(systemconfig.SystemType("ts")).GetAllJVMMethods()
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

	got, err := GetSystemCache(systemconfig.SystemType("ts")).GetAllDatabaseOperations()
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

	got, err := GetSystemCache(systemconfig.SystemType("otel-demo")).GetAllHTTPEndpoints()
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

	cache := GetSystemCache(systemconfig.SystemType("ts"))
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

// TestDBBacked_CacheMetric_HitMiss proves the per-system snapshot counter
// classifies the first GetAllX as a miss and every subsequent reuse within
// the same warm-up as a hit. ResetSystemCache forces a new warm-up.
func TestDBBacked_CacheMetric_HitMiss(t *testing.T) {
	store := newStubStore()
	withChaosStore(t, store)

	miss := chaosPointsCacheTotal.WithLabelValues("ts", "miss")
	hit := chaosPointsCacheTotal.WithLabelValues("ts", "hit")
	missBefore := testutil.ToFloat64(miss)
	hitBefore := testutil.ToFloat64(hit)

	cache := GetSystemCache(systemconfig.SystemType("ts"))
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

	if got := testutil.ToFloat64(miss) - missBefore; got != 1 {
		t.Errorf("want 1 miss across 5 GetAllX in one warm-up, got %v", got)
	}
	if got := testutil.ToFloat64(hit) - hitBefore; got != 4 {
		t.Errorf("want 4 hits across 5 GetAllX in one warm-up, got %v", got)
	}
	if n := atomic.LoadInt64(&store.queryCount); n != 1 {
		t.Errorf("metric/QueryPoints disagree: want 1 SELECT, got %d", n)
	}

	ResetSystemCache(systemconfig.SystemType("ts"))
	if _, err := GetSystemCache(systemconfig.SystemType("ts")).GetAllHTTPEndpoints(); err != nil {
		t.Fatalf("http (post-reset): %v", err)
	}
	if got := testutil.ToFloat64(miss) - missBefore; got != 2 {
		t.Errorf("post-reset: want 2 cumulative misses, got %v", got)
	}
}

// TestDBBacked_CrossProcessInvalidation exercises the LatestUpdate probe that
// drives cross-process cache invalidation (issue #459). Sub-cases cover every
// edge case the spec calls out: first-miss bootstrap, fresh probe (no-op),
// import-bumped probe (invalidate + re-extract), tombstone via supersede,
// empty-system first import, and probe error (serve stale).
func TestDBBacked_CrossProcessInvalidation(t *testing.T) {
	t0 := time.Unix(1700000000, 0)

	tests := []struct {
		name          string
		setup         func(t *testing.T, store *stubStore)
		mutate        func(t *testing.T, store *stubStore)
		wantBefore    int
		wantAfter     int
		wantQueries   int64 // QueryPoints calls expected across both reads
		wantInvalKind string
	}{
		{
			name: "fresh_probe_no_invalidation",
			setup: func(_ *testing.T, store *stubStore) {
				store.setLatest("ts", t0)
			},
			mutate: func(_ *testing.T, store *stubStore) {
				store.setLatest("ts", t0) // unchanged
			},
			wantBefore:    2,
			wantAfter:     2,
			wantQueries:   1,
			wantInvalKind: "fresh",
		},
		{
			name: "new_import_advances_high_water_mark",
			setup: func(_ *testing.T, store *stubStore) {
				store.setLatest("ts", t0)
			},
			mutate: func(_ *testing.T, store *stubStore) {
				// Operator imports a new HTTP point: row count grows, MAX(updated_at) bumps.
				rows := append([]ChaosPointRow{}, store.rows["ts"]...)
				rows = append(rows, ChaosPointRow{
					SystemName: "ts", CapabilityName: "http_response_abort",
					Target: map[string]any{
						"app": "ts-new-service", "method": "GET", "path": "/api/new", "port": float64(8080),
					},
				})
				store.setRows("ts", rows)
				store.setLatest("ts", t0.Add(time.Minute))
			},
			wantBefore:    2,
			wantAfter:     3,
			wantQueries:   2,
			wantInvalKind: "stale",
		},
		{
			name: "supersede_bumps_updated_at_without_row_growth",
			setup: func(_ *testing.T, store *stubStore) {
				store.setLatest("ts", t0)
			},
			mutate: func(_ *testing.T, store *stubStore) {
				// Supersede doesn't change row count visible to QueryPoints (which
				// filters status='active'); it only flips updated_at on the row
				// that turned 'superseded'. The probe still catches it.
				store.setLatest("ts", t0.Add(time.Minute))
			},
			wantBefore:    2,
			wantAfter:     2,
			wantQueries:   2, // probe forced a re-query even though rows are identical
			wantInvalKind: "stale",
		},
		{
			name: "tombstone_via_delete_bumps_max",
			setup: func(_ *testing.T, store *stubStore) {
				store.setLatest("ts", t0)
			},
			mutate: func(_ *testing.T, store *stubStore) {
				// Delete strips one HTTP row.
				kept := make([]ChaosPointRow, 0, len(store.rows["ts"]))
				for _, r := range store.rows["ts"] {
					if r.CapabilityName == "http_request_replace_path" {
						continue // drop the ts-order-service endpoint
					}
					kept = append(kept, r)
				}
				store.setRows("ts", kept)
				store.setLatest("ts", t0.Add(time.Minute))
			},
			wantBefore:    2,
			wantAfter:     1,
			wantQueries:   2,
			wantInvalKind: "stale",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newStubStore()
			tc.setup(t, store)
			withChaosStore(t, store)

			cache := GetSystemCache(systemconfig.SystemType("ts"))
			before, err := cache.GetAllHTTPEndpoints()
			if err != nil {
				t.Fatalf("first GetAllHTTPEndpoints: %v", err)
			}
			if len(before) != tc.wantBefore {
				t.Fatalf("before: want %d endpoints, got %d: %#v", tc.wantBefore, len(before), before)
			}

			invalBefore := testutil.ToFloat64(chaosPointsInvalidateTotal.WithLabelValues("ts", tc.wantInvalKind))
			tc.mutate(t, store)

			after, err := cache.GetAllHTTPEndpoints()
			if err != nil {
				t.Fatalf("second GetAllHTTPEndpoints: %v", err)
			}
			if len(after) != tc.wantAfter {
				t.Fatalf("after: want %d endpoints, got %d: %#v", tc.wantAfter, len(after), after)
			}

			if n := atomic.LoadInt64(&store.queryCount); n != tc.wantQueries {
				t.Errorf("want %d QueryPoints calls, got %d", tc.wantQueries, n)
			}
			if got := testutil.ToFloat64(chaosPointsInvalidateTotal.WithLabelValues("ts", tc.wantInvalKind)) - invalBefore; got < 1 {
				t.Errorf("want at least 1 invalidation outcome=%q recorded by second read, got delta=%v", tc.wantInvalKind, got)
			}
		})
	}
}

// TestDBBacked_FirstMissBootstrap proves the very first GetAllX call after
// process boot still does exactly one QueryPoints — the probe seeds the
// high-water mark instead of racing the warm-up into a second SELECT.
func TestDBBacked_FirstMissBootstrap(t *testing.T) {
	store := newStubStore()
	store.setLatest("ts", time.Unix(1700000000, 0))
	withChaosStore(t, store)

	if _, err := GetSystemCache(systemconfig.SystemType("ts")).GetAllHTTPEndpoints(); err != nil {
		t.Fatalf("GetAllHTTPEndpoints: %v", err)
	}
	if n := atomic.LoadInt64(&store.queryCount); n != 1 {
		t.Errorf("first miss should issue exactly 1 QueryPoints, got %d", n)
	}
	if n := atomic.LoadInt64(&store.latestUpdateCount); n != 1 {
		t.Errorf("first miss should issue exactly 1 LatestUpdate probe, got %d", n)
	}
}

// TestDBBacked_EmptySystem_FirstImportInvalidates exercises the edge case
// where a system starts with zero rows: LatestUpdate returns the zero time,
// the cache stores an empty derived slice, and the very first import for
// that system bumps the high-water mark from epoch to a real timestamp,
// invalidating the empty cache.
func TestDBBacked_EmptySystem_FirstImportInvalidates(t *testing.T) {
	store := &stubStore{rows: map[string][]ChaosPointRow{}, latestPerSystem: map[string]time.Time{}}
	withChaosStore(t, store)

	sysType := systemconfig.SystemType("greenfield")
	t.Cleanup(func() { ResetSystemCache(sysType) })

	cache := GetSystemCache(sysType)
	got, err := cache.GetAllHTTPEndpoints()
	if err != nil {
		t.Fatalf("GetAllHTTPEndpoints (empty system): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 endpoints for empty system, got %d", len(got))
	}

	// First import lands.
	store.setRows("greenfield", []ChaosPointRow{
		{SystemName: "greenfield", CapabilityName: "http_response_abort", Target: map[string]any{
			"app": "g-svc", "method": "GET", "path": "/health", "port": float64(8080),
		}},
	})
	store.setLatest("greenfield", time.Unix(1700000000, 0))

	got, err = cache.GetAllHTTPEndpoints()
	if err != nil {
		t.Fatalf("GetAllHTTPEndpoints (post-import): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("first import should be visible without restart, got %d endpoints", len(got))
	}
}

// TestDBBacked_ProbeError_ServesStale proves a transient probe failure does
// not break the guided pipeline — we keep returning the cached snapshot and
// classify the outcome as probe_error in the metric.
func TestDBBacked_ProbeError_ServesStale(t *testing.T) {
	store := newStubStore()
	store.setLatest("ts", time.Unix(1700000000, 0))
	withChaosStore(t, store)

	cache := GetSystemCache(systemconfig.SystemType("ts"))
	first, err := cache.GetAllHTTPEndpoints()
	if err != nil {
		t.Fatalf("first GetAllHTTPEndpoints: %v", err)
	}

	probeErrBefore := testutil.ToFloat64(chaosPointsInvalidateTotal.WithLabelValues("ts", "probe_error"))
	store.mu.Lock()
	store.latestErr = context.DeadlineExceeded
	store.mu.Unlock()

	second, err := cache.GetAllHTTPEndpoints()
	if err != nil {
		t.Fatalf("GetAllHTTPEndpoints during probe outage: %v", err)
	}
	if len(second) != len(first) {
		t.Errorf("probe error must not change cached payload: before=%d after=%d", len(first), len(second))
	}
	if got := testutil.ToFloat64(chaosPointsInvalidateTotal.WithLabelValues("ts", "probe_error")) - probeErrBefore; got < 1 {
		t.Errorf("want probe_error counter to advance, got delta=%v", got)
	}
}
