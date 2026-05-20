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

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
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

// capturingLogger returns a logger whose output is appended to buf so tests
// can assert on the emitted WARN / INFO substrings.
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

// flag=in_process → chaos service lister never called; resolution goes
// through the DB lookup seam.
func TestCatalogPreflight_FlagInProcess_SkipsLister(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set(catalogSourceFlagKey, catalogSourceInProc)
	viper.Set("chaos.service_url", "http://example.invalid")

	var listerCalls int32
	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		atomic.AddInt32(&listerCalls, 1)
		return "", 0, nil
	}
	var lookupCalls int32
	var gotSys, gotSvc, gotCap string
	lookup := func(ctx context.Context, system, service, capability string) (string, error) {
		atomic.AddInt32(&lookupCalls, 1)
		gotSys, gotSvc, gotCap = system, service, capability
		return "ts:ts-order-service::seed:pod_kill::abc", nil
	}
	var buf bytes.Buffer
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, capturingLogger(&buf), lister, lookup)
	if atomic.LoadInt32(&listerCalls) != 0 {
		t.Fatalf("chaos-service lister invoked despite in_process flag (calls=%d)", listerCalls)
	}
	if atomic.LoadInt32(&lookupCalls) != 1 {
		t.Fatalf("expected DB lookup invoked once, got %d", lookupCalls)
	}
	if gotSys != "ts" || gotSvc != "ts-order-service" || gotCap != "pod_kill" {
		t.Fatalf("unexpected lookup args system=%q service=%q capability=%q", gotSys, gotSvc, gotCap)
	}
	if !strings.Contains(buf.String(), "catalog source: in_process") {
		t.Fatalf("expected INFO confirming in_process hit, got: %s", buf.String())
	}
}

// flag=in_process + a recording HTTP server: zero requests must reach the
// network — soak guarantee that chaos-service outage does not block
// submission.
func TestCatalogPreflight_FlagInProcess_NoHTTPCalls(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set(catalogSourceFlagKey, catalogSourceInProc)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	viper.Set("chaos.service_url", srv.URL)

	lookup := func(ctx context.Context, system, service, capability string) (string, error) {
		return "ts:ts-order-service::seed:pod_kill::abc", nil
	}
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, testLogger(), nil, lookup)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("expected no HTTP calls under in_process, got %d", got)
	}
}

// flag=chaos_service + URL empty → silent override, lister not called.
func TestCatalogPreflight_FlagChaosService_NoURL_SilentOverride(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set(catalogSourceFlagKey, catalogSourceChaos)

	var calls int32
	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		atomic.AddInt32(&calls, 1)
		return "", 0, nil
	}
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, testLogger(), lister, nil)
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("lister invoked with empty chaos.service_url (calls=%d)", calls)
	}
}

// flag=chaos_service + lister returns a Point → lister called with the
// mapped capability.
func TestCatalogPreflight_FlagChaosService_PointFound(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set(catalogSourceFlagKey, catalogSourceChaos)
	viper.Set("chaos.service_url", "http://example.invalid")

	var gotSystem, gotService, gotCapability string
	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		gotSystem, gotService, gotCapability = system, service, capability
		return "ts:ts-order-service::seed:pod_kill::abc", http.StatusOK, nil
	}
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, testLogger(), lister, nil)
	if gotSystem != "ts" || gotService != "ts-order-service" || gotCapability != "pod_kill" {
		t.Fatalf("unexpected lister args system=%q service=%q capability=%q",
			gotSystem, gotService, gotCapability)
	}
}

// flag=chaos_service + lister returns 5xx → WARN log emitted with the
// fallback substring and inject continues (no error propagation).
func TestCatalogPreflight_FlagChaosService_FiveXX_LogsFallback(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set(catalogSourceFlagKey, catalogSourceChaos)
	viper.Set("chaos.service_url", "http://example.invalid")

	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		return "", http.StatusServiceUnavailable, errors.New("chaos service returned 503")
	}
	var buf bytes.Buffer
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, capturingLogger(&buf), lister, nil)
	if !strings.Contains(buf.String(), "falling back to in-process") {
		t.Fatalf("expected fallback WARN, got: %s", buf.String())
	}
}

// flag=chaos_service + lister returns no points → WARN log emitted with the
// point-not-found substring; inject continues via in-process resolution.
func TestCatalogPreflight_FlagChaosService_PointNotFound_LogsWarn(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set(catalogSourceFlagKey, catalogSourceChaos)
	viper.Set("chaos.service_url", "http://example.invalid")

	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		return "", http.StatusNotFound, nil
	}
	var buf bytes.Buffer
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, capturingLogger(&buf), lister, nil)
	if !strings.Contains(buf.String(), "point not found in chaos service catalog") {
		t.Fatalf("expected point-not-found WARN, got: %s", buf.String())
	}
}

// lister gets a context with deadline ≤ catalogPreflightTimeout.
func TestCatalogPreflight_AppliesTimeoutToCallContext(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set(catalogSourceFlagKey, catalogSourceChaos)
	viper.Set("chaos.service_url", "http://example.invalid")

	var deadline time.Time
	var hasDeadline bool
	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		deadline, hasDeadline = ctx.Deadline()
		return "", 0, errors.New("timeout sentinel")
	}
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, testLogger(), lister, nil)
	if !hasDeadline {
		t.Fatal("expected per-call context to carry a deadline")
	}
	if remaining := time.Until(deadline); remaining > catalogPreflightTimeout+time.Second {
		t.Fatalf("deadline too far in the future: %v", remaining)
	}
}

// SDK lister attaches Authorization: Bearer <token> when
// CHAOS_OUTBOUND_BEARER is set and omits the header when unset. This is
// the step-5b R1 outbound-signing contract — covered against the same
// httptest stub the SDKLister_HTTPTest case uses.
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
		// sync.Once is process-global; reset so the INFO log fires for THIS test.
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

// otel-demo / otel-demo0 disambiguation: when the catalog is keyed by the
// logical system name ("otel-demo") and the runtime workload runs in a
// pool-allocated ns ("otel-demo0"), the preflight must query the catalog by
// the *logical* name and find the seeded Point — no WARN-fallback.
//
// Regression for the structural mismatch fixed in step 5b R3: passing
// payload.namespace ("otel-demo0") instead of payload.system ("otel-demo")
// to the lister would have caused every preflight to log
// "point not found in chaos service catalog" even on a correctly populated
// catalog.
func TestCatalogPreflight_LogicalSystem_NotConcreteNamespace(t *testing.T) {
	resetCatalogFlags(t)

	var mu sync.Mutex
	var gotSystemPathParam string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		// Path shape: /v1beta/systems/{sys}/points
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

	viper.Set(catalogSourceFlagKey, catalogSourceChaos)
	viper.Set("chaos.service_url", srv.URL)

	configs := []guidedcli.GuidedConfig{{
		System:    "otel-demo",
		App:       "cart",
		ChaosType: "PodKill",
	}}

	var buf bytes.Buffer
	// Pass the logical system "otel-demo" — NOT the runtime ns "otel-demo0".
	runCatalogPreflight(context.Background(), "otel-demo", configs, nil, capturingLogger(&buf), nil, nil)

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

// httptest-backed end-to-end: SDK-driven lister against a stub
// /v1beta/systems/{sys}/points endpoint mimicking the chaos service.
func TestCatalogPreflight_SDKLister_HTTPTest(t *testing.T) {
	resetCatalogFlags(t)

	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		// Wrap in dto.GenericResponse shape: {"data": {points: [...], total: 1, limit: 50, offset: 0}}
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

	viper.Set(catalogSourceFlagKey, catalogSourceChaos)
	viper.Set("chaos.service_url", srv.URL)

	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), nil, testLogger(), nil, nil)

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
