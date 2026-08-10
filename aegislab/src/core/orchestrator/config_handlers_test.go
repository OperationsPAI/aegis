package consumer

import (
	"context"
	"strconv"
	"testing"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
)

// newTestLimiter builds a TokenBucketRateLimiter without a Redis store. The
// config-reload path (GetConfig/UpdateConfig) only touches in-memory fields,
// so a nil store is safe for these tests.
func newTestLimiter(serviceName string, maxTokens int) *TokenBucketRateLimiter {
	return &TokenBucketRateLimiter{
		maxTokens:   maxTokens,
		waitTimeout: time.Duration(consts.TokenWaitTimeout) * time.Second,
		serviceName: serviceName,
	}
}

// setIntConfig mirrors what the etcd watcher does before invoking the handler:
// SetViperValue writes the fresh value to viper so the handler's config.GetInt
// reads it back.
func setIntConfig(t *testing.T, key string, value int) {
	t.Helper()
	if err := config.SetViperValue(key, strconv.Itoa(value), consts.ConfigValueTypeInt); err != nil {
		t.Fatalf("SetViperValue(%s): %v", key, err)
	}
}

// TestRestartLimiterReloadsOnConsistentKey is the regression guard for the
// watched-key / read-key / const drift that left `aegisctl etcd put
// rate_limiting.max_concurrent_restarts_pedestal` a silent no-op: the watcher
// case said "rate_limiting.max_concurrent_restarts" while the read used the
// _pedestal-suffixed const, so the switch fell to default and the limiter
// never moved. With the case keyed on consts.MaxTokensKeyRestartPedestal a put
// on that key must change the live limiter's max tokens.
func TestRestartLimiterReloadsOnConsistentKey(t *testing.T) {
	restart := newTestLimiter(consts.RestartPedestalServiceName, consts.MaxConcurrentRestartPedestal)
	h := newRateLimitingConfigHandler(nil, restart, nil, nil, nil, nil, nil)

	setIntConfig(t, consts.MaxTokensKeyRestartPedestal, 30)

	if err := h.Handle(context.Background(), consts.MaxTokensKeyRestartPedestal, "2", "30"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got, _ := restart.GetConfig()
	if got != 30 {
		t.Fatalf("restart limiter max tokens = %d after reload, want 30", got)
	}
}

// TestRestartLimiterIgnoresLegacyKey locks in that the pre-fix literal
// ("rate_limiting.max_concurrent_restarts", no _pedestal) is NOT what the
// handler matches — it hits the default branch and leaves the limiter
// untouched. If someone reintroduces the old key as the watcher case this
// fails, surfacing the mismatch instead of shipping a dead apply path.
func TestRestartLimiterIgnoresLegacyKey(t *testing.T) {
	restart := newTestLimiter(consts.RestartPedestalServiceName, consts.MaxConcurrentRestartPedestal)
	h := newRateLimitingConfigHandler(nil, restart, nil, nil, nil, nil, nil)

	if err := h.Handle(context.Background(), "rate_limiting.max_concurrent_restarts", "2", "30"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got, _ := restart.GetConfig()
	if got != consts.MaxConcurrentRestartPedestal {
		t.Fatalf("restart limiter changed on legacy key: max tokens = %d, want %d",
			got, consts.MaxConcurrentRestartPedestal)
	}
}

// TestNamespaceWarmingLimiterReloads covers the ns_warming key, which had a
// correct watcher case but no seed metadata row (so a runtime put was
// rejected upstream). The handler wiring itself must still apply the value
// when the key fires.
func TestNamespaceWarmingLimiterReloads(t *testing.T) {
	warming := newTestLimiter(consts.NamespaceWarmingServiceName, consts.MaxConcurrentNamespaceWarming)
	h := newRateLimitingConfigHandler(nil, nil, nil, warming, nil, nil, nil)

	setIntConfig(t, consts.MaxTokensKeyNamespaceWarming, 75)

	if err := h.Handle(context.Background(), consts.MaxTokensKeyNamespaceWarming, "30", "75"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got, _ := warming.GetConfig()
	if got != 75 {
		t.Fatalf("warming limiter max tokens = %d after reload, want 75", got)
	}
}

// TestReconcileRateLimitersFromConfig is the regression guard for the
// const-only boot break: limiters are fx-constructed before the config
// listener loads etcd/DB overrides into viper, so a fresh worker would honour
// only the in-binary const until a live UpdateConfig that never comes. After
// the scopes are activated (viper populated), reconcile must push the
// already-set override into each limiter without any watch event.
func TestReconcileRateLimitersFromConfig(t *testing.T) {
	restart := newTestLimiter(consts.RestartPedestalServiceName, consts.MaxConcurrentRestartPedestal)

	// Limiter still holds the boot const; an override is present in config
	// (as if loaded from etcd/DB at scope activation) but never delivered via
	// a watch event.
	setIntConfig(t, consts.MaxTokensKeyRestartPedestal, 10)

	ReconcileRateLimitersFromConfig(restart, nil, nil, nil, nil, nil)

	got, _ := restart.GetConfig()
	if got != 10 {
		t.Fatalf("restart limiter max tokens = %d after reconcile, want 10 (still const-bound)", got)
	}
}
