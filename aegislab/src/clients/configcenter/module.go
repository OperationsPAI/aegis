package configcenterclient

import (
	"fmt"

	"aegis/platform/config"
	"aegis/crud/admin/configcenter"

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
)

func provideClient(
	center configcenter.Center, // available iff module/configcenter is also loaded
	tokenSrc TokenSource, // available iff app wires an adapter
) (Client, error) {
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
