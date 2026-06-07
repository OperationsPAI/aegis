package consumer

import (
	"context"
	"strconv"
	"testing"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
)

// setRestartIntConfig writes an int dynamic-config value the same way the etcd
// watcher does (viper.Set under the full dotted key) so restartMaxTokensFor /
// config.GetInt read it back.
func setRestartIntConfig(t *testing.T, key string, value int) {
	t.Helper()
	if err := config.SetViperValue(key, strconv.Itoa(value), consts.ConfigValueTypeInt); err != nil {
		t.Fatalf("SetViperValue(%s): %v", key, err)
	}
}

func newRegistry() *RestartLimiterRegistry {
	// nil redis gateway is safe: For()/Reconcile() only touch in-memory
	// limiter fields (bucketKey, maxTokens). The Redis store is exercised
	// only by Acquire/Release, which these tests don't call.
	fallback := newTestLimiter(consts.RestartPedestalServiceName, consts.MaxConcurrentRestartPedestal)
	return NewRestartLimiterRegistry(nil, fallback)
}

// TestRestartRegistry_PerSystemBucketsAreIndependent is the core property: each
// system resolves to its OWN limiter instance with its OWN Redis bucket key, so
// exhausting one system's tokens can never consume another's. The
// TokenBucketStore.Acquire script is SCARD-scoped to bucketKey, so distinct
// keys are physically independent sets — this asserts the keys differ (and
// differ from the global bucket), which is what guarantees no cross-starvation.
func TestRestartRegistry_PerSystemBucketsAreIndependent(t *testing.T) {
	reg := newRegistry()

	ts := reg.For("ts")
	sn := reg.For("sn")

	if ts.bucketKey == sn.bucketKey {
		t.Fatalf("ts and sn share a bucket key %q — not independent", ts.bucketKey)
	}
	wantTS := consts.RestartPedestalTokenBucket + ":ts"
	if ts.bucketKey != wantTS {
		t.Fatalf("ts bucket key = %q, want %q", ts.bucketKey, wantTS)
	}
	wantSN := consts.RestartPedestalTokenBucket + ":sn"
	if sn.bucketKey != wantSN {
		t.Fatalf("sn bucket key = %q, want %q", sn.bucketKey, wantSN)
	}
	if ts.bucketKey == consts.RestartPedestalTokenBucket || sn.bucketKey == consts.RestartPedestalTokenBucket {
		t.Fatalf("per-system bucket collides with the global bucket key")
	}
}

// TestRestartRegistry_TsOverrideVsGlobalDefault locks in the requested
// behavior: ts is capped at its per-system override (5) while an unconfigured
// system inherits the global default. Changing the ts value must not move the
// unconfigured system, and vice versa.
func TestRestartRegistry_TsOverrideVsGlobalDefault(t *testing.T) {
	setRestartIntConfig(t, consts.MaxTokensKeyRestartPedestal, 40)
	setRestartIntConfig(t, restartPerSystemKey("ts"), 5)

	reg := newRegistry()

	ts := reg.For("ts")
	if got, _ := ts.GetConfig(); got != 5 {
		t.Fatalf("ts max tokens = %d, want 5 (per-system override)", got)
	}

	// A system with no override inherits the global default (40), NOT ts's 5.
	media := reg.For("media")
	if got, _ := media.GetConfig(); got != 40 {
		t.Fatalf("media max tokens = %d, want 40 (global default)", got)
	}
}

// TestRestartRegistry_EmptySystemUsesFallback ensures we never silently share a
// per-system bucket for an unknown/empty system — an empty system resolves to
// the global fallback bucket (the same instance fx wires as restart_limiter).
func TestRestartRegistry_EmptySystemUsesFallback(t *testing.T) {
	reg := newRegistry()
	if got := reg.For(""); got != reg.fallback {
		t.Fatalf("empty system did not resolve to the global fallback limiter")
	}
	if got := reg.For("   "); got != reg.fallback {
		t.Fatalf("whitespace-only system did not resolve to the global fallback limiter")
	}
}

// TestRestartRegistry_SameSystemCached verifies a system maps to one shared
// limiter instance — two restarts of the same system must contend for the same
// bucket, not get fresh independent pools each time.
func TestRestartRegistry_SameSystemCached(t *testing.T) {
	reg := newRegistry()
	first := reg.For("ts")
	second := reg.For("ts")
	if first != second {
		t.Fatalf("repeated For(ts) returned different instances; bucket would not be shared per system")
	}
}

// TestRestartRegistry_ReconcileSystemAppliesOverride covers the live-config
// path: after a per-system bucket exists, pushing a new ts override and calling
// ReconcileSystem must move only that system's live cap.
func TestRestartRegistry_ReconcileSystemAppliesOverride(t *testing.T) {
	setRestartIntConfig(t, consts.MaxTokensKeyRestartPedestal, 40)
	setRestartIntConfig(t, restartPerSystemKey("ts"), 5)

	reg := newRegistry()
	ts := reg.For("ts")
	if got, _ := ts.GetConfig(); got != 5 {
		t.Fatalf("ts initial max = %d, want 5", got)
	}

	setRestartIntConfig(t, restartPerSystemKey("ts"), 9)
	reg.ReconcileSystem("ts")
	if got, _ := ts.GetConfig(); got != 9 {
		t.Fatalf("ts max after ReconcileSystem = %d, want 9", got)
	}
}

// TestRestartRegistry_GlobalChangeFlowsToUnoverriddenSystems verifies that
// bumping the global default and calling Reconcile re-applies it to a
// per-system bucket that has NO override of its own, while leaving an
// overridden system (ts) pinned to its override.
func TestRestartRegistry_GlobalChangeFlowsToUnoverriddenSystems(t *testing.T) {
	setRestartIntConfig(t, consts.MaxTokensKeyRestartPedestal, 40)
	setRestartIntConfig(t, restartPerSystemKey("ts"), 5)

	reg := newRegistry()
	media := reg.For("media") // inherits global default (40)
	ts := reg.For("ts")       // pinned to override (5)

	setRestartIntConfig(t, consts.MaxTokensKeyRestartPedestal, 25)
	reg.Reconcile()

	if got, _ := media.GetConfig(); got != 25 {
		t.Fatalf("media max after global change = %d, want 25", got)
	}
	if got, _ := ts.GetConfig(); got != 5 {
		t.Fatalf("ts max after global change = %d, want 5 (override must hold)", got)
	}
}

// TestRestartHandler_PerSystemKeyDoesNotClobberGlobal is the regression guard
// for the viper nested-key trap that drove the key convention: writing the
// per-system override (rate_limiting.max_concurrent_restarts_pedestal_per_
// system.ts) must NOT overwrite the scalar at the global key. If the
// per-system key were nested directly under the global key
// (...restarts_pedestal.ts), viper would merge it into a map and the global
// read would return 0. This asserts the global stays intact and the handler
// routes the per-system key to ReconcileSystem (not the default branch).
func TestRestartHandler_PerSystemKeyDoesNotClobberGlobal(t *testing.T) {
	setRestartIntConfig(t, consts.MaxTokensKeyRestartPedestal, 40)

	reg := newRegistry()
	ts := reg.For("ts") // created at default (40) before any override
	h := newRateLimitingConfigHandler(nil, reg.fallback, reg, nil, nil, nil, nil)

	// Simulate the watcher: write viper, then dispatch the per-system key.
	setRestartIntConfig(t, restartPerSystemKey("ts"), 5)
	if err := h.Handle(context.Background(), restartPerSystemKey("ts"), "40", "5"); err != nil {
		t.Fatalf("Handle(per-system key): %v", err)
	}

	if got := config.GetInt(consts.MaxTokensKeyRestartPedestal); got != 40 {
		t.Fatalf("global restart key clobbered by per-system write: got %d, want 40", got)
	}
	if got, _ := ts.GetConfig(); got != 5 {
		t.Fatalf("ts limiter not updated by per-system key dispatch: got %d, want 5", got)
	}
}

// TestRestartHandler_GlobalKeyFlowsToRegistry verifies that dispatching the
// GLOBAL restart key re-applies the new default to a per-system bucket with no
// override (via the registry), not just the standalone fallback limiter.
func TestRestartHandler_GlobalKeyFlowsToRegistry(t *testing.T) {
	setRestartIntConfig(t, consts.MaxTokensKeyRestartPedestal, 40)

	reg := newRegistry()
	media := reg.For("media") // inherits global default
	h := newRateLimitingConfigHandler(nil, reg.fallback, reg, nil, nil, nil, nil)

	setRestartIntConfig(t, consts.MaxTokensKeyRestartPedestal, 18)
	if err := h.Handle(context.Background(), consts.MaxTokensKeyRestartPedestal, "40", "18"); err != nil {
		t.Fatalf("Handle(global key): %v", err)
	}

	if got, _ := media.GetConfig(); got != 18 {
		t.Fatalf("media (no override) did not track global change via registry: got %d, want 18", got)
	}
}

// TestRestartRegistry_UpdateTimeout pushes a new wait timeout across all known
// buckets (fallback + per-system).
func TestRestartRegistry_UpdateTimeout(t *testing.T) {
	reg := newRegistry()
	ts := reg.For("ts")

	reg.UpdateTimeout(42 * time.Second)

	if _, to := ts.GetConfig(); to != 42*time.Second {
		t.Fatalf("ts wait timeout = %v, want 42s", to)
	}
	if _, to := reg.fallback.GetConfig(); to != 42*time.Second {
		t.Fatalf("fallback wait timeout = %v, want 42s", to)
	}
}
