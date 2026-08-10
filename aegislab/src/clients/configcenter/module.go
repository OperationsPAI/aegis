package configcenterclient

import (
	"context"
	"fmt"

	"aegis/platform/config"
	"aegis/crud/admin/configcenter"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

// Module wires a configcenterclient.Client into the fx graph. The
// concrete implementation is chosen by `[configcenter.client] mode`:
//
//	mode = "local"  → LocalClient over module/configcenter
//	mode = "remote" → RemoteClient over HTTP+SSE
//
// Consumers always inject `configcenterclient.Client`.
var Module = fx.Module("configcenterclient",
	fx.Provide(provideClient),
	fx.Invoke(registerDynamicViper),
)

// tokenSrcParams declares tokenSrc as optional so binaries that load the
// Module but don't wire an SSO TokenSource (e.g. consumers running with
// configcenter.client.mode=local) still satisfy provideClient's graph.
type tokenSrcParams struct {
	fx.In
	Source TokenSource `optional:"true"`
}

// registerDynamicViper attaches an OnStart hook that mirrors the "aegis"
// configcenter namespace into viper so legacy `config.GetString` callers
// observe live etcd values. Best-effort: a failed bootstrap logs WARN and
// the process continues (TOML / env defaults remain authoritative).
func registerDynamicViper(lc fx.Lifecycle, c Client) {
	var stop func()
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			s, err := BootstrapDynamicViper(ctx, c, DynamicViperNamespace)
			if err != nil {
				logrus.WithError(err).
					Warn("configcenterclient: dynamic viper bootstrap failed; static config remains authoritative")
				return nil
			}
			stop = s
			return nil
		},
		OnStop: func(context.Context) error {
			if stop != nil {
				stop()
			}
			return nil
		},
	})
}

func provideClient(
	center configcenter.Center, // available iff module/configcenter is also loaded
	tokenP tokenSrcParams,
) (Client, error) {
	tokenSrc := tokenP.Source
	mode := config.GetString("configcenter.client.mode")
	if mode == "" {
		mode = "local"
	}
	switch mode {
	case "local":
		if center == nil {
			return nil, fmt.Errorf("configcenterclient mode=local but no configcenter.Center in graph")
		}
		return NewLocalClient(center), nil
	case "remote":
		base := config.GetString("configcenter.client.endpoint")
		if base == "" {
			return nil, fmt.Errorf("configcenterclient mode=remote requires [configcenter.client] endpoint")
		}
		return NewRemoteClient(RemoteClientConfig{BaseURL: base}, tokenSrc)
	default:
		return nil, fmt.Errorf("configcenterclient: unknown mode %q (want local|remote)", mode)
	}
}
