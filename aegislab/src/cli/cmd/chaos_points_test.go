package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestChaosPointsList_FiltersForwarded asserts every CLI flag becomes the
// matching query-string param on GET /v1beta/systems/{sys}/points. A
// regression here would silently widen the listing — a real risk because
// the SDK builder swallows unknown query names without erroring.
func TestChaosPointsList_FiltersForwarded(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotQuery  url.Values
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{` +
			`"points":[{"id":"abc1234567890def","system_name":"otel-demo","service_name":"cart","capability_name":"pod_kill","status":"active","source":"import","target":{"app":"cart","namespace":"otel-demo"},"created_at":"2026-05-19T00:00:00Z","updated_at":"2026-05-19T00:00:00Z"}],` +
			`"total":1,"limit":50,"offset":10}}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	chaosPointsSystem = "otel-demo"
	chaosPointsService = "cart"
	chaosPointsCapability = "pod_kill"
	chaosPointsStatus = "active"
	chaosPointsLimit = 50
	chaosPointsOffset = 10

	if err := runChaosPointsList(nil, nil); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q; want GET", gotMethod)
	}
	if gotPath != "/v1beta/systems/otel-demo/points" {
		t.Errorf("path = %q; want /v1beta/systems/otel-demo/points", gotPath)
	}
	want := map[string]string{
		"service":    "cart",
		"capability": "pod_kill",
		"status":     "active",
		"limit":      "50",
		"offset":     "10",
	}
	for k, v := range want {
		if got := gotQuery.Get(k); got != v {
			t.Errorf("query[%q] = %q; want %q", k, got, v)
		}
	}
}

// TestChaosPointsList_EmptyResult asserts the renderer copes with an
// empty points list (no panic on nil-deref of Total/Limit/Offset).
func TestChaosPointsList_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{"points":[],"total":0,"limit":100,"offset":0}}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()
	chaosPointsSystem = "empty-sys"

	if err := runChaosPointsList(nil, nil); err != nil {
		t.Fatalf("empty list failed: %v", err)
	}
}

// TestChaosPointsList_RequiresSystem guards the precondition check —
// system must be set before reaching the SDK.
func TestChaosPointsList_RequiresSystem(t *testing.T) {
	chaosTestSetup(t, "http://127.0.0.1:1")
	defer resetChaosFlags()
	if err := runChaosPointsList(nil, nil); err == nil {
		t.Fatal("expected error when --system missing")
	}
}
