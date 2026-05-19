package consumer

import (
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

func sampleConfigs() []guidedcli.GuidedConfig {
	return []guidedcli.GuidedConfig{{
		System:    "ts",
		App:       "ts-order-service",
		ChaosType: "PodKill",
	}}
}

// flag=in_process → lister never called.
func TestCatalogPreflight_FlagInProcess_SkipsLister(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set(catalogSourceFlagKey, catalogSourceInProc)
	viper.Set("chaos.service_url", "http://example.invalid")

	var calls int32
	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		atomic.AddInt32(&calls, 1)
		return "", 0, nil
	}
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), testLogger(), lister)
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("lister invoked despite in_process flag (calls=%d)", calls)
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
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), testLogger(), lister)
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
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), testLogger(), lister)
	if gotSystem != "ts" || gotService != "ts-order-service" || gotCapability != "pod_kill" {
		t.Fatalf("unexpected lister args system=%q service=%q capability=%q",
			gotSystem, gotService, gotCapability)
	}
}

// flag=chaos_service + lister returns 5xx → does not panic, no error
// propagation (informational only).
func TestCatalogPreflight_FlagChaosService_FiveXX_DoesNotBlock(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set(catalogSourceFlagKey, catalogSourceChaos)
	viper.Set("chaos.service_url", "http://example.invalid")

	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		return "", http.StatusServiceUnavailable, errors.New("chaos service returned 503")
	}
	// Must not panic.
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), testLogger(), lister)
}

// flag=chaos_service + lister returns no points → WARN branch (still no
// blocking error).
func TestCatalogPreflight_FlagChaosService_PointNotFound(t *testing.T) {
	resetCatalogFlags(t)
	viper.Set(catalogSourceFlagKey, catalogSourceChaos)
	viper.Set("chaos.service_url", "http://example.invalid")

	lister := func(ctx context.Context, system, service, capability string) (string, int, error) {
		return "", http.StatusNotFound, nil
	}
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), testLogger(), lister)
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
	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), testLogger(), lister)
	if !hasDeadline {
		t.Fatal("expected per-call context to carry a deadline")
	}
	if remaining := time.Until(deadline); remaining > catalogPreflightTimeout+time.Second {
		t.Fatalf("deadline too far in the future: %v", remaining)
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

	runCatalogPreflight(context.Background(), "ts", sampleConfigs(), testLogger(), nil)

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
