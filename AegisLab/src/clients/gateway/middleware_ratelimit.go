package gateway

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// TODO(phase-D): when module/ratelimiter exposes an in-process,
// DB-free limiter (or a Redis-backed mode wired separately from the
// admin token-bucket Service), swap this minimal token bucket for
// that implementation so operators can introspect/reset gateway
// buckets through the same admin API.

// tokenBucket is a tiny lock-protected token bucket used per route.
// rps is steady-state, burst is max bucket size; refill is continuous.
type tokenBucket struct {
	mu     sync.Mutex
	rps    float64
	burst  float64
	tokens float64
	last   time.Time
}

func newBucket(rps float64, burst int) *tokenBucket {
	if burst <= 0 {
		burst = int(rps)
	}
	if burst <= 0 {
		burst = 1
	}
	return &tokenBucket{rps: rps, burst: float64(burst), tokens: float64(burst), last: time.Now()}
}

func (b *tokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	delta := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += delta * b.rps
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	if b.tokens >= 1 {
		b.tokens -= 1
		return true
	}
	return false
}

// RateLimiter holds a global default + per-route overrides.
type RateLimiter struct {
	global *tokenBucket
	routes map[string]*tokenBucket
}

// NewRateLimiter pre-builds buckets for each route's override (keyed by
// route prefix). The global bucket is used when no per-route override
// is set. A zero RPS disables that bucket (Allow always returns true).
func NewRateLimiter(global RateLimitPolicy, routes []Route) *RateLimiter {
	rl := &RateLimiter{
		routes: make(map[string]*tokenBucket, len(routes)),
	}
	if global.RPS > 0 {
		rl.global = newBucket(global.RPS, global.Burst)
	}
	for _, r := range routes {
		if r.RateLimit.RPS > 0 {
			rl.routes[r.Prefix] = newBucket(r.RateLimit.RPS, r.RateLimit.Burst)
		}
	}
	return rl
}

// Middleware enforces the per-route bucket (falling back to the global
// bucket) and writes RFC-style RateLimit headers.
func (rl *RateLimiter) Middleware(route *Route, next http.Handler) http.Handler {
	bucket := rl.routes[route.Prefix]
	if bucket == nil {
		bucket = rl.global
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bucket == nil {
			w.Header().Set("X-RateLimit-Limit", "unlimited")
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("X-RateLimit-Limit", strconv.FormatFloat(bucket.rps, 'f', -1, 64))
		w.Header().Set("X-RateLimit-Burst", strconv.Itoa(int(bucket.burst)))
		if !bucket.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, fmt.Sprintf("rate limit exceeded: %g rps", bucket.rps), http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
