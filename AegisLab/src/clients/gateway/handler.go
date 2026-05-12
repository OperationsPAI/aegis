package gateway

import (
	"net/http"
	"strings"
)

// Handler is the root http.Handler for the gateway. It matches the
// route, builds the middleware chain (logging → cors → rate-limit →
// auth → proxy), and dispatches.
type Handler struct {
	routes  *RouteTable
	proxies *ProxyPool
	auth    *Authenticator
	rl      *RateLimiter
	cors    CORSConfig
}

// NewHandler wires the matched-route dispatch pipeline. It is the
// single object the http.Server hangs off.
func NewHandler(routes *RouteTable, proxies *ProxyPool, auth *Authenticator, rl *RateLimiter, cfg Config) *Handler {
	return &Handler{
		routes:  routes,
		proxies: proxies,
		auth:    auth,
		rl:      rl,
		cors:    cfg.CORS,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	// TODO(phase-A): /readyz probes a configurable subset of upstreams.
	if r.URL.Path == "/readyz" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
		return
	}

	route := h.routes.Match(r.URL.Path)
	if route == nil {
		http.NotFound(w, r)
		return
	}

	if route.StripPrefix {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, route.Prefix)
		if !strings.HasPrefix(r.URL.Path, "/") {
			r.URL.Path = "/" + r.URL.Path
		}
	}

	proxy, err := h.proxies.For(route.Upstream, route.Timeout())
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}

	chain := h.auth.Middleware(route, proxy)
	chain = h.rl.Middleware(route, chain)
	chain = CORSMiddleware(h.cors, chain)
	chain = LoggingMiddleware(route, chain)
	chain.ServeHTTP(w, r)
}
