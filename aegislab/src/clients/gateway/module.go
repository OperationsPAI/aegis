package gateway

import (
	"context"

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
		newAuditSink,
		NewHandler,
	),
)

func newRouteTable(cfg Config) *RouteTable {
	return NewRouteTable(cfg.Routes)
}

func newAuditSink(lc fx.Lifecycle, cfg Config) *AuditSink {
	s := NewAuditSink(cfg.Audit)
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error { return s.Close() },
	})
	return s
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
