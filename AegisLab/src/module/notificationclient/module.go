package notificationclient

import (
	"fmt"

	"aegis/platform/config"
	"aegis/module/notification"

	"go.uber.org/fx"
)

// Module wires a notificationclient.Client into the fx graph. The
// concrete implementation is chosen by `[notification] mode`:
//
//	mode = "local"  (default) → LocalClient over notification.Publisher
//	mode = "remote"           → RemoteClient over HTTP
//
// Producers always inject `notificationclient.Client`. They never
// learn which mode is in effect.
var Module = fx.Module("notificationclient",
	fx.Provide(provideClient),
)

func provideClient(
	pub notification.Publisher, // available iff the notification module is also loaded
	tokenSrc TokenSource, // available iff app wires an adapter
) (Client, error) {
	mode := config.GetString("notification.mode")
	if mode == "" {
		mode = "local"
	}
	switch mode {
	case "local":
		if pub == nil {
			return nil, fmt.Errorf("notificationclient mode=local but no notification.Publisher in graph")
		}
		return NewLocalClient(pub), nil
	case "remote":
		base := config.GetString("notification.remote.base_url")
		if base == "" {
			return nil, fmt.Errorf("notificationclient mode=remote requires [notification.remote] base_url")
		}
		return NewRemoteClient(RemoteClientConfig{
			BaseURL:    base,
			MaxRetries: config.GetInt("notification.remote.max_retries"),
		}, tokenSrc)
	default:
		return nil, fmt.Errorf("notificationclient: unknown mode %q (want local|remote)", mode)
	}
}
