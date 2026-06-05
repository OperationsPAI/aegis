package gateway

import (
	"aegis/clients/sso"
	"aegis/platform/jwtkeys"

	"go.uber.org/fx"
)

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

type authenticatorParams struct {
	fx.In
	Config Config
	Client *ssoclient.Client
	Signer *jwtkeys.Signer `optional:"true"`
}

func newAuthenticator(p authenticatorParams) (*Authenticator, error) {
	return NewAuthenticator(p.Client, p.Config.TrustedHeaderKey, p.Signer)
}
