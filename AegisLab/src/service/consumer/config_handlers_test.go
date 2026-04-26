package consumer

import (
	"context"
	"strings"
	"sync"
	"testing"

	"aegis/consts"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/spf13/viper"
)

// fakeSyncer records register / unregister calls so tests can assert against
// the sequence chaosSystemHandler drives.
type fakeSyncer struct {
	mu         sync.Mutex
	registered map[string]chaos.SystemConfig
	events     []string
}

func newFakeSyncer() *fakeSyncer {
	return &fakeSyncer{registered: make(map[string]chaos.SystemConfig)}
}

func (f *fakeSyncer) IsRegistered(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.registered[name]
	return ok
}

func (f *fakeSyncer) Register(cfg chaos.SystemConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered[cfg.Name] = cfg
	f.events = append(f.events, "register:"+cfg.Name+":"+cfg.NsPattern+":"+cfg.AppLabelKey)
	return nil
}

func (f *fakeSyncer) Update(cfg chaos.SystemConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered[cfg.Name] = cfg
	f.events = append(f.events, "update:"+cfg.Name+":"+cfg.NsPattern+":"+cfg.AppLabelKey)
	return nil
}

func (f *fakeSyncer) Unregister(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.registered, name)
	f.events = append(f.events, "unregister:"+name)
	return nil
}

func (f *fakeSyncer) lastEvent() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.events) == 0 {
		return ""
	}
	return f.events[len(f.events)-1]
}

// withFakeSyncer swaps the package-level registrySyncer for the duration of
// a single test.
func withFakeSyncer(t *testing.T, fs *fakeSyncer) {
	t.Helper()
	orig := registrySyncer
	registrySyncer = fs
	t.Cleanup(func() { registrySyncer = orig })
}

// seedViperSystem pushes a full set of injection.system.<name>.* keys into
// Viper. The ChaosSystemConfigManager reads Viper on demand so no explicit
// reload is required after seeding.
func seedViperSystem(t *testing.T, name, nsPattern, appLabelKey, displayName string, count int, status consts.StatusType) {
	t.Helper()

	viper.Reset()
	viper.Set("injection.system."+name+".count", count)
	viper.Set("injection.system."+name+".ns_pattern", nsPattern)
	viper.Set("injection.system."+name+".extract_pattern", "^("+name+")(\\d+)$")
	viper.Set("injection.system."+name+".display_name", displayName)
	viper.Set("injection.system."+name+".app_label_key", appLabelKey)
	viper.Set("injection.system."+name+".is_builtin", false)
	viper.Set("injection.system."+name+".status", int(status))
}

func TestChaosSystemCategoryHandlerReportsGlobalScope(t *testing.T) {
	h := newChaosSystemHandler(nil, nil, nil)
	for _, category := range chaosSystemCategories() {
		handler := h.forCategory(category)
		if got := handler.Scope(); got != consts.ConfigScopeGlobal {
			t.Fatalf("category %q scope = %v, want %v (Global)", category, got, consts.ConfigScopeGlobal)
		}
		if handler.Category() != category {
			t.Fatalf("Category() = %q, want %q", handler.Category(), category)
		}
	}
}

func TestParseInjectionSystemKey(t *testing.T) {
	cases := []struct {
		in           string
		wantSystem   string
		wantField    string
		wantSelected bool
	}{
		{"injection.system.ts.count", "ts", "count", true},
		{"injection.system.otel-demo.status", "otel-demo", "status", true},
		{"injection.system.ts.app_label_key", "ts", "app_label_key", true},
		{"algo.detector", "", "", false},
		{"injection.system.", "", "", false},
	}
	for _, tc := range cases {
		sys, field := parseInjectionSystemKey(tc.in)
		selected := sys != ""
		if selected != tc.wantSelected || sys != tc.wantSystem || field != tc.wantField {
			t.Errorf("parseInjectionSystemKey(%q) = (%q, %q), want (%q, %q)",
				tc.in, sys, field, tc.wantSystem, tc.wantField)
		}
	}
}

func TestChaosSystemHandlerRegistersOnNewKey(t *testing.T) {
	fs := newFakeSyncer()
	withFakeSyncer(t, fs)

	seedViperSystem(t, "ts", "^ts\\d+$", "app", "Train Ticket", 3, consts.CommonEnabled)

	h := newChaosSystemHandler(nil, nil, nil)
	if err := h.reconcile(context.Background(), "injection.system.ts.status", "", "1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !fs.IsRegistered("ts") {
		t.Fatalf("expected ts to be registered after reconcile")
	}
	if last := fs.lastEvent(); !strings.HasPrefix(last, "register:ts:") {
		t.Fatalf("last event = %q, want register:ts:...", last)
	}
}

func TestChaosSystemHandlerUnregistersOnDisable(t *testing.T) {
	fs := newFakeSyncer()
	withFakeSyncer(t, fs)

	// Start with the system registered.
	seedViperSystem(t, "ts", "^ts\\d+$", "app", "Train Ticket", 3, consts.CommonEnabled)
	h := newChaosSystemHandler(nil, nil, nil)
	if err := h.reconcile(context.Background(), "injection.system.ts.status", "", "1"); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	if !fs.IsRegistered("ts") {
		t.Fatalf("expected ts to be registered after initial reconcile")
	}

	// Flip status to disabled.
	seedViperSystem(t, "ts", "^ts\\d+$", "app", "Train Ticket", 3, consts.CommonDisabled)
	if err := h.reconcile(context.Background(), "injection.system.ts.status", "1", "0"); err != nil {
		t.Fatalf("disable reconcile: %v", err)
	}
	if fs.IsRegistered("ts") {
		t.Fatalf("expected ts to be unregistered after disable")
	}
}

func TestChaosSystemHandlerReRegistersOnNsPatternChange(t *testing.T) {
	fs := newFakeSyncer()
	withFakeSyncer(t, fs)

	seedViperSystem(t, "ts", "^ts\\d+$", "app", "Train Ticket", 3, consts.CommonEnabled)
	h := newChaosSystemHandler(nil, nil, nil)
	if err := h.reconcile(context.Background(), "injection.system.ts.status", "", "1"); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}

	// Change ns_pattern.
	seedViperSystem(t, "ts", "^trainticket\\d+$", "app", "Train Ticket", 3, consts.CommonEnabled)
	if err := h.reconcile(context.Background(), "injection.system.ts.ns_pattern", "^ts\\d+$", "^trainticket\\d+$"); err != nil {
		t.Fatalf("ns_pattern reconcile: %v", err)
	}
	last := fs.lastEvent()
	// Re-registration must happen as an in-place Update so attached metadata
	// providers survive (issue #129). A plain register: event would mean we
	// regressed to Unregister+Register and wiped the providers.
	if !strings.HasPrefix(last, "update:ts:^trainticket") {
		t.Fatalf("last event = %q, want update:ts:^trainticket...", last)
	}
	for _, ev := range fs.events {
		if strings.HasPrefix(ev, "unregister:ts") {
			t.Fatalf("unexpected unregister during pattern update: events=%v", fs.events)
		}
	}
}

func TestChaosSystemHandlerIgnoresUnrelatedKey(t *testing.T) {
	fs := newFakeSyncer()
	withFakeSyncer(t, fs)

	seedViperSystem(t, "ts", "^ts\\d+$", "app", "Train Ticket", 3, consts.CommonEnabled)

	h := newChaosSystemHandler(nil, nil, nil)
	if err := h.reconcile(context.Background(), "rate_limiting.max_concurrent_builds", "3", "5"); err != nil {
		t.Fatalf("reconcile unrelated key: %v", err)
	}
	if fs.IsRegistered("ts") {
		t.Fatalf("unrelated key should not trigger registration")
	}
}

// TestChaosSystemReloadDoesNotHitDB asserts the reconcile flow has no *gorm.DB
// dependency. The package no longer imports gorm for this path, but we keep
// the test as documentation and as a guard against regressions that would
// reintroduce a DB round trip.
func TestChaosSystemReloadDoesNotHitDB(t *testing.T) {
	seedViperSystem(t, "ts", "^ts\\d+$", "app", "Train Ticket", 3, consts.CommonEnabled)

	fs := newFakeSyncer()
	withFakeSyncer(t, fs)

	h := newChaosSystemHandler(nil, nil, nil)
	if err := h.reconcile(context.Background(), "injection.system.ts.count", "3", "5"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

// TestRateLimitingConfigHandlerCategory pins the consumer-scope category so a
// future refactor that drops it from RegisterConsumerHandlers' watcher set
// would break this test rather than silently stop reload from firing.
func TestRateLimitingConfigHandlerCategory(t *testing.T) {
	h := newRateLimitingConfigHandler(nil, nil, nil, nil, nil, nil)
	if got := h.Category(); got != "rate_limiting" {
		t.Fatalf("Category() = %q, want %q", got, "rate_limiting")
	}
	if got := h.Scope(); got != consts.ConfigScopeConsumer {
		t.Fatalf("Scope() = %v, want %v", got, consts.ConfigScopeConsumer)
	}
}

// TestRateLimitingConfigHandlerHandlesBuildDatapackKey covers the new
// "rate_limiting.max_concurrent_build_datapack" switch case. With a nil
// limiter the handler must remain a no-op (no panic), and unknown keys
// must hit the default branch without erroring.
func TestRateLimitingConfigHandlerHandlesBuildDatapackKey(t *testing.T) {
	h := newRateLimitingConfigHandler(nil, nil, nil, nil, nil, nil)
	if err := h.Handle(context.Background(), "rate_limiting.max_concurrent_build_datapack", "8", "12"); err != nil {
		t.Fatalf("handle build_datapack key: %v", err)
	}
	if err := h.Handle(context.Background(), "rate_limiting.unknown_key", "", ""); err != nil {
		t.Fatalf("handle unknown key (must default-no-op): %v", err)
	}
}

// TestRateLimitingConfigKeyAlignsWithBuildContainerConst guards against the
// historical mismatch between the watched key (max_concurrent_builds) and the
// const the reload reads (previously max_concurrent_build_container, which
// was never present in etcd). If they ever drift again, the watcher fires
// and the reload silently no-ops against a default value.
func TestRateLimitingConfigKeyAlignsWithBuildContainerConst(t *testing.T) {
	const watchedKey = "rate_limiting.max_concurrent_builds"
	if consts.MaxTokensKeyBuildContainer != watchedKey {
		t.Fatalf("MaxTokensKeyBuildContainer = %q, want %q (must match the key the rate_limiting handler watches)",
			consts.MaxTokensKeyBuildContainer, watchedKey)
	}
}
