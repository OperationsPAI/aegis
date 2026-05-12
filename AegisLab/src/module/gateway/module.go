package gateway

import (
	"aegis/module/ssoclient"

	"go.uber.org/fx"
)

// Module wires the gateway primitives. It depends on ssoclient.Module
// from the caller (wired in app/gateway/options.go) for the JWT
// verifier.
//
// Provides:
//   - Config         (loaded from viper's [gateway] section)
//   - *RouteTable    (sorted, normalized)
//   - *ProxyPool     (one reverse proxy per upstream, lazily built)
//   - *RateLimiter   (in-process token buckets, see middleware_ratelimit.go TODO)
//   - *Authenticator (HMAC-signed trusted-header injection)
//   - *Handler       (root http.Handler)
var Module = fx.Module("gateway",
	fx.Provide(
		LoadConfig,
		newRouteTable,
		NewProxyPool,
		newRateLimiter,
		newAuthenticator,
		NewHandler,
	),
)

func newRouteTable(cfg Config) *RouteTable {
	return NewRouteTable(cfg.Routes)
}

func newRateLimiter(cfg Config) *RateLimiter {
	return NewRateLimiter(cfg.RateLimit, cfg.Routes)
}

func newAuthenticator(cfg Config, client *ssoclient.Client) *Authenticator {
	return NewAuthenticator(client, cfg.TrustedHeaderKey)
}
