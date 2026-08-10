package consumer

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
	redis "aegis/platform/redis"

	"github.com/sirupsen/logrus"
)

// RestartLimiterRegistry hands out a per-system restart-pedestal token bucket
// so that one slow-restarting system (e.g. ts) cannot starve every other
// system of restart slots. Each system gets its own Redis bucket key
// (`token_bucket:restart_service:<system>`) and its own max-tokens bound:
//
//   - if a per-system override config exists
//     (rate_limiting.max_concurrent_restarts_pedestal_per_system.<system>),
//     that wins;
//   - otherwise the system inherits the global default
//     (rate_limiting.max_concurrent_restarts_pedestal), so unconfigured
//     systems behave exactly as they did under the single global bucket.
//
// The registry keeps the original global limiter (the one fx still wires as
// `restart_limiter`) as the fallback bucket for restarts whose system is
// unknown/empty. It never silently lets two systems share a bucket.
type RestartLimiterRegistry struct {
	gateway *redis.Gateway

	mu       sync.Mutex
	bySystem map[string]*TokenBucketRateLimiter
	// fallback is the global bucket used when the restart's system is empty
	// or unknown. It is also the same instance fx exposes as restart_limiter
	// (so admin status / stuck-trace reconcile keep seeing it).
	fallback *TokenBucketRateLimiter
}

// NewRestartLimiterRegistry builds the registry around the existing global
// restart limiter. fallback is the fx-provided restart_limiter singleton.
func NewRestartLimiterRegistry(gateway *redis.Gateway, fallback *TokenBucketRateLimiter) *RestartLimiterRegistry {
	return &RestartLimiterRegistry{
		gateway:  gateway,
		bySystem: make(map[string]*TokenBucketRateLimiter),
		fallback: fallback,
	}
}

// For returns the restart limiter that gates restarts of the given system.
// An empty system maps to the global fallback bucket. Per-system limiters are
// created lazily and cached; concurrent callers share one instance per system.
func (r *RestartLimiterRegistry) For(system string) *TokenBucketRateLimiter {
	system = strings.TrimSpace(system)
	if system == "" {
		logrus.Warn("restart limiter requested for empty system; using global fallback bucket")
		return r.fallback
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if l, ok := r.bySystem[system]; ok {
		return l
	}

	limiter := newSystemRestartLimiter(r.gateway, system)
	r.bySystem[system] = limiter
	return limiter
}

// Reconcile re-applies the current config to every known per-system bucket
// plus the fallback. Called after a global-key change so a system without its
// own override tracks the new global default.
func (r *RestartLimiterRegistry) Reconcile() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fallback != nil {
		_, timeout := r.fallback.GetConfig()
		if secs := config.GetInt(consts.TokenWaitTimeoutKey); secs > 0 {
			timeout = time.Duration(secs) * time.Second
		}
		r.fallback.UpdateConfig(restartMaxTokensFor(""), timeout)
	}
	for system, limiter := range r.bySystem {
		_, timeout := limiter.GetConfig()
		if secs := config.GetInt(consts.TokenWaitTimeoutKey); secs > 0 {
			timeout = time.Duration(secs) * time.Second
		}
		limiter.UpdateConfig(restartMaxTokensFor(system), timeout)
	}
}

// ReconcileSystem re-applies config to a single system's bucket. Called when a
// per-system override key fires. If that system has no bucket yet (no restart
// has run for it), there is nothing live to update — the next For() will pick
// up the override at creation time.
func (r *RestartLimiterRegistry) ReconcileSystem(system string) {
	system = strings.TrimSpace(system)
	if system == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	limiter, ok := r.bySystem[system]
	if !ok {
		return
	}
	_, timeout := limiter.GetConfig()
	if secs := config.GetInt(consts.TokenWaitTimeoutKey); secs > 0 {
		timeout = time.Duration(secs) * time.Second
	}
	limiter.UpdateConfig(restartMaxTokensFor(system), timeout)
}

// UpdateTimeout pushes a new wait timeout to every known bucket (global +
// per-system). Used by the token_wait_timeout config handler.
func (r *RestartLimiterRegistry) UpdateTimeout(timeout time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fallback != nil {
		maxTokens, _ := r.fallback.GetConfig()
		r.fallback.UpdateConfig(maxTokens, timeout)
	}
	for _, limiter := range r.bySystem {
		maxTokens, _ := limiter.GetConfig()
		limiter.UpdateConfig(maxTokens, timeout)
	}
}

// restartMaxTokensFor resolves a system's restart cap: its per-system override
// when set, otherwise the global default, finally the in-binary const. An
// empty system resolves the global default (the fallback bucket).
func restartMaxTokensFor(system string) int {
	system = strings.TrimSpace(system)
	if system != "" {
		if v := config.GetInt(restartPerSystemKey(system)); v > 0 {
			return v
		}
	}
	if v := config.GetInt(consts.MaxTokensKeyRestartPedestal); v > 0 {
		return v
	}
	return consts.MaxConcurrentRestartPedestal
}

// restartPerSystemKey builds the dotted dynamic-config key for a system's
// per-system restart override.
func restartPerSystemKey(system string) string {
	return consts.MaxTokensKeyRestartPedestalPerSystemPrefix + "." + system
}

// restartSystemFromPerSystemKey extracts the system short-code from a
// per-system override key, or "" if key is not a per-system override.
func restartSystemFromPerSystemKey(key string) string {
	prefix := consts.MaxTokensKeyRestartPedestalPerSystemPrefix + "."
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	return strings.TrimPrefix(key, prefix)
}

// resolveRestartLimiter picks the restart limiter for a given system. It
// prefers the per-system registry bucket; when the registry is absent (older
// test wiring) it falls back to the global RestartRateLimiter so behaviour is
// unchanged.
func resolveRestartLimiter(deps RuntimeDeps, system string) *TokenBucketRateLimiter {
	if deps.RestartLimiterRegistry != nil {
		return deps.RestartLimiterRegistry.For(system)
	}
	return deps.RestartRateLimiter
}

// newSystemRestartLimiter constructs a per-system restart limiter with its own
// Redis bucket key and a max resolved from config.
func newSystemRestartLimiter(gateway *redis.Gateway, system string) *TokenBucketRateLimiter {
	bucketKey := fmt.Sprintf("%s:%s", consts.RestartPedestalTokenBucket, system)

	waitTimeout := config.GetInt(consts.TokenWaitTimeoutKey)
	if waitTimeout <= 0 {
		waitTimeout = consts.TokenWaitTimeout
	}

	return newTokenBucketRateLimiterWithBucket(gateway, rateLimiterInstance{
		bucketKey:   bucketKey,
		maxTokens:   restartMaxTokensFor(system),
		waitTimeout: time.Duration(waitTimeout) * time.Second,
		serviceName: fmt.Sprintf("%s:%s", consts.RestartPedestalServiceName, system),
	})
}
