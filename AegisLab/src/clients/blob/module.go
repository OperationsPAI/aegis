package blobclient

import (
	"fmt"

	"aegis/platform/config"
	"aegis/crud/storage/blob"

	"go.uber.org/fx"
)

// Module wires a blobclient.Client into the fx graph. The concrete
// implementation is chosen by `[blob.client] mode`:
//
//	mode = "local"  (default) → LocalClient over module/blob.Service
//	mode = "remote"           → RemoteClient over HTTP
//
// Producers always inject blobclient.Client and learn nothing about
// which mode is in effect.
var Module = fx.Module("blobclient",
	fx.Provide(provideClient),
)

// clientDeps lets `Svc` and `TokenSrc` resolve as optional dependencies —
// LocalClient never needs a TokenSource (no HTTP auth), and RemoteClient
// tolerates a nil source for unauthenticated calls. Same shape lets
// in-process mode work without dragging blob.Module into every fx graph.
type clientDeps struct {
	fx.In
	Svc      *blob.Service `optional:"true"`
	TokenSrc TokenSource   `optional:"true"`
}

func provideClient(deps clientDeps) (Client, error) {
	mode := config.GetString("blob.client.mode")
	if mode == "" {
		mode = "local"
	}
	switch mode {
	case "local":
		if deps.Svc == nil {
			return nil, fmt.Errorf("blobclient mode=local but no blob.Service in graph")
		}
		return NewLocalClient(deps.Svc), nil
	case "remote":
		base := config.GetString("blob.client.endpoint")
		if base == "" {
			return nil, fmt.Errorf("blobclient mode=remote requires [blob.client] endpoint")
		}
		return NewRemoteClient(RemoteClientConfig{
			BaseURL:    base,
			MaxRetries: config.GetInt("blob.client.max_retries"),
		}, deps.TokenSrc)
	default:
		return nil, fmt.Errorf("blobclient: unknown mode %q (want local|remote)", mode)
	}
}
