package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	guidedcli "aegis/platform/chaos"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func resetCatalogFlags(t *testing.T) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
}

func testLogger() *logrus.Entry {
	l := logrus.New()
	l.SetOutput(&strings.Builder{})
	return logrus.NewEntry(l)
}

func capturingLogger(buf *bytes.Buffer) *logrus.Entry {
	l := logrus.New()
	l.SetOutput(buf)
	l.SetLevel(logrus.InfoLevel)
	return logrus.NewEntry(l)
}

func sampleConfigs() []guidedcli.GuidedConfig {
	return []guidedcli.GuidedConfig{{
		System:    "ts",
		App:       "ts-order-service",
		ChaosType: "PodKill",
	}}
}

// URL empty → silent override, lister not called.
func TestCatalogPreflight_NoURL_SilentOverride(t *testing.T) {
	resetCatalogFlags(t)

	var calls int32
	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		atomic.AddInt32(&calls, 1)
		return "", 0, nil
	}
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, testLogger(), lister)
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("lister invoked with empty chaos.service_url (calls=%d)", calls)
	}
}

// lister returns a Point → lister called with the mapped capability.
func TestCatalogPreflight_PointFound(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set("chaos.service_url", "http://example.invalid")

	var gotSystem, gotService, gotCapability string
	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		gotSystem, gotService, gotCapability = system, service, capability
		return "ts:ts-order-service::seed:pod_kill::abc", http.StatusOK, nil
	}
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, testLogger(), lister)
	if gotSystem != "ts" || gotService != "ts-order-service" || gotCapability != "pod_kill" {
		t.Fatalf("unexpected lister args system=%q service=%q capability=%q",
			gotSystem, gotService, gotCapability)
	}
}

// lister returns 5xx → WARN log emitted with the fallback substring and
// inject continues (no error propagation).
func TestCatalogPreflight_FiveXX_LogsFallback(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set("chaos.service_url", "http://example.invalid")

	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		return "", http.StatusServiceUnavailable, errors.New("chaos service returned 503")
	}
	var buf bytes.Buffer
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, capturingLogger(&buf), lister)
	if !strings.Contains(buf.String(), "falling back to in-process") {
		t.Fatalf("expected fallback WARN, got: %s", buf.String())
	}
}

// lister returns no points → WARN log emitted with the point-not-found
// substring; inject continues.
func TestCatalogPreflight_PointNotFound_LogsWarn(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set("chaos.service_url", "http://example.invalid")

	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		return "", http.StatusNotFound, nil
	}
	var buf bytes.Buffer
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, capturingLogger(&buf), lister)
	if !strings.Contains(buf.String(), "point not found in chaos service catalog") {
		t.Fatalf("expected point-not-found WARN, got: %s", buf.String())
	}
}

// lister gets a context with deadline ≤ catalogPreflightTimeout.
func TestCatalogPreflight_AppliesTimeoutToCallContext(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set("chaos.service_url", "http://example.invalid")

	var deadline time.Time
	var hasDeadline bool
	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		deadline, hasDeadline = ctx.Deadline()
		return "", 0, errors.New("timeout sentinel")
	}
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, testLogger(), lister)
	if !hasDeadline {
		t.Fatal("expected per-call context to carry a deadline")
	}
	if remaining := time.Until(deadline); remaining > catalogPreflightTimeout+time.Second {
		t.Fatalf("deadline too far in the future: %v", remaining)
	}
}

// SDK lister attaches Authorization: Bearer <token> when
// CHAOS_OUTBOUND_BEARER is set and omits the header when unset.
func TestCatalogPreflight_SDKLister_OutboundBearer(t *testing.T) {
	t.Run("env set → header attached", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"points": []map[string]any{{"id": "stub-point", "capability": "pod_kill"}},
					"total":  1, "limit": 50, "offset": 0,
				},
			})
		}))
		defer srv.Close()

		t.Setenv("CHAOS_OUTBOUND_BEARER", "outbound-token")
		outboundBearerAttachOnce = sync.Once{}

		lister := newSDKPointsLister(srv.URL, testLogger())
		if _, _, err := lister(context.Background(), "ts", "ts-order-service", "pod_kill"); err != nil {
			t.Fatalf("lister err: %v", err)
		}
		if gotAuth != "Bearer outbound-token" {
			t.Fatalf("expected Authorization=Bearer outbound-token, got %q", gotAuth)
		}
	})

	t.Run("env unset → no header", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"points": []map[string]any{}, "total": 0, "limit": 50, "offset": 0},
			})
		}))
		defer srv.Close()

		t.Setenv("CHAOS_OUTBOUND_BEARER", "")
		outboundBearerAttachOnce = sync.Once{}

		lister := newSDKPointsLister(srv.URL, testLogger())
		_, _, _ = lister(context.Background(), "ts", "ts-order-service", "pod_kill")
		if gotAuth != "" {
			t.Fatalf("expected no Authorization header when env unset, got %q", gotAuth)
		}
	})
}

// otel-demo / otel-demo0 disambiguation regression: preflight must query
// catalog by logical system name, not the pool-allocated ns.
func TestCatalogPreflight_LogicalSystem_NotConcreteNamespace(t *testing.T) {
	resetCatalogFlags(t)

	var mu sync.Mutex
	var gotSystemPathParam string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 3 && parts[0] == "v1beta" && parts[1] == "systems" {
			gotSystemPathParam = parts[2]
		}
		mu.Unlock()
		body := map[string]any{
			"data": map[string]any{
				"points": []map[string]any{{
					"id":         "otel-demo:cart::seed:pod_kill::abc12345",
					"capability": r.URL.Query().Get("capability"),
				}},
				"total": 1, "limit": 50, "offset": 0,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	viper.Set("chaos.service_url", srv.URL)

	configs := []guidedcli.GuidedConfig{{
		System:    "otel-demo",
		App:       "cart",
		ChaosType: "PodKill",
	}}

	var buf bytes.Buffer
	runCatalogPreflight(context.Background(), "otel-demo", configs, nil, capturingLogger(&buf), nil)

	mu.Lock()
	defer mu.Unlock()
	if gotSystemPathParam != "otel-demo" {
		t.Fatalf("expected chaos service queried with logical name 'otel-demo', got %q", gotSystemPathParam)
	}
	out := buf.String()
	if strings.Contains(out, "falling back to in-process") || strings.Contains(out, "point not found") {
		t.Fatalf("expected catalog hit (no WARN-fallback), got logs: %s", out)
	}
	if !strings.Contains(out, "catalog source: chaos_service") {
		t.Fatalf("expected INFO confirming catalog hit, got logs: %s", out)
	}
}

// httptest-backed end-to-end: SDK-driven lister against a stub endpoint.
func TestCatalogPreflight_SDKLister_HTTPTest(t *testing.T) {
	resetCatalogFlags(t)

	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		body := map[string]any{
			"data": map[string]any{
				"points": []map[string]any{{"id": "stub-point", "capability": r.URL.Query().Get("capability")}},
				"total":  1,
				"limit":  50,
				"offset": 0,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	viper.Set("chaos.service_url", srv.URL)

	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, testLogger(), nil)

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 {
		t.Fatalf("expected exactly one HTTP call, got %d: %v", len(paths), paths)
	}
	if !strings.HasPrefix(paths[0], "/v1beta/systems/ts/points") {
		t.Fatalf("unexpected path: %s", paths[0])
	}
	if !strings.Contains(paths[0], "capability=pod_kill") {
		t.Fatalf("expected capability=pod_kill in query, got %s", paths[0])
	}
	if !strings.Contains(paths[0], "service=ts-order-service") {
		t.Fatalf("expected service=ts-order-service in query, got %s", paths[0])
	}
}
