package configcenterclient

import (
	"context"

	"aegis/module/configcenter"
)

// LocalClient wraps an in-process configcenter.Center. Used by the
// configcenter binary itself and any dev/test build where everything
// runs in one process.
type LocalClient struct {
	center configcenter.Center
}

func NewLocalClient(center configcenter.Center) *LocalClient {
	return &LocalClient{center: center}
}

func (c *LocalClient) Bind(ctx context.Context, namespace, key string, out any, opts ...BindOpt) (Handle, error) {
	return c.center.Bind(ctx, namespace, key, out, opts...)
}

func (c *LocalClient) Get(ctx context.Context, namespace, key string) ([]byte, Layer, error) {
	return c.center.Get(ctx, namespace, key)
}

func (c *LocalClient) Set(ctx context.Context, namespace, key string, value any, _ ...SetOpt) error {
	// LocalClient bypasses the audit writer on purpose — there's no
	// HTTP request, no actor to record. Audit-bearing writes go
	// through the remote (HTTP) path.
	return c.center.Set(ctx, namespace, key, value)
}

func (c *LocalClient) Delete(ctx context.Context, namespace, key string) error {
	return c.center.Delete(ctx, namespace, key)
}

func (c *LocalClient) List(ctx context.Context, namespace string) ([]Entry, error) {
	return c.center.List(ctx, namespace)
}

var _ Client = (*LocalClient)(nil)
