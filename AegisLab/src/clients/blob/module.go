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

func provideClient(
	svc *blob.Service, // available iff module/blob is also loaded
	tokenSrc TokenSource, // available iff app wires an adapter
) (Client, error) {
	mode := config.GetString("blob.client.mode")
	if mode == "" {
		mode = "local"
	}
	switch mode {
	case "local":
		if svc == nil {
			return nil, fmt.Errorf("blobclient mode=local but no blob.Service in graph")
		}
		return NewLocalClient(svc), nil
	case "remote":
		base := config.GetString("blob.client.endpoint")
		if base == "" {
			return nil, fmt.Errorf("blobclient mode=remote requires [blob.client] endpoint")
		}
		return NewRemoteClient(RemoteClientConfig{
			BaseURL:    base,
			MaxRetries: config.GetInt("blob.client.max_retries"),
		}, tokenSrc)
	default:
		return nil, fmt.Errorf("blobclient: unknown mode %q (want local|remote)", mode)
	}
}
