// Package configcenterclient is the consumer-side SDK for the
// configuration center. Every service except `aegis-configcenter`
// itself uses this package; it never touches etcd directly for
// config purposes.
//
// Two interchangeable implementations:
//
//   - LocalClient — wraps the in-process `module/configcenter`. Used
//     by `aegis-configcenter` itself and by dev builds where every
//     service runs in-process.
//   - RemoteClient — HTTP + SSE client against `aegis-configcenter`.
//     Default for production services. Carries a service token from
//     ssoclient.
//
// Switching deployment mode is a config flip
// (`[configcenter.client] mode = "local"|"remote"`) — no consumer
// code changes.
package configcenterclient

import (
	"context"

	"aegis/module/configcenter"
)

// Re-export the small surface so consumers depend on this package
// only (mirrors notificationclient pattern).
type (
	Layer = configcenter.Layer
	Entry = configcenter.Entry

	BindOpt = configcenter.BindOpt
	SetOpt  = configcenter.SetOpt

	Handle = configcenter.Handle
)

// WithDefault re-exports configcenter.WithDefault so callers can
// stay on this package.
func WithDefault(v any) BindOpt { return configcenter.WithDefault(v) }

// WithValidator re-exports configcenter.WithValidator.
func WithValidator(fn func(any) error) BindOpt { return configcenter.WithValidator(fn) }

// WithSchemaVersion re-exports configcenter.WithSchemaVersion.
func WithSchemaVersion(v int) BindOpt { return configcenter.WithSchemaVersion(v) }

// Client is the only type consumers reference.
type Client interface {
	Bind(ctx context.Context, namespace, key string, out any, opts ...BindOpt) (Handle, error)
	Get(ctx context.Context, namespace, key string) (raw []byte, layer Layer, err error)
	Set(ctx context.Context, namespace, key string, value any, opts ...SetOpt) error
	Delete(ctx context.Context, namespace, key string) error
	List(ctx context.Context, namespace string) ([]Entry, error)
}

// TokenSource produces a fresh Bearer token for cross-service calls
// to `aegis-configcenter`. The app wiring layer supplies an adapter
// — typically a thin wrapper over ssoclient.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}
