// Package httpclient is the shared HTTP plumbing for the producer-side
// remote clients in clients/{blob,notification,configcenter}. It exists
// to deduplicate the otelhttp transport + Bearer injection + base-URL
// joining that those clients all do identically; retry policy and
// response decoding stay in the per-domain caller because they differ.
//
// Not used by clients/sso (which carries its own service-token cache)
// or clients/runtime (gRPC).
package httpclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// TokenSource yields a Bearer token for cross-service calls. The shape
// matches the per-package TokenSource interfaces (blob, notification,
// configcenter) so existing wirings keep type-checking.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Config builds a Client. BaseURL is required; Timeout defaults to 5s.
type Config struct {
	BaseURL     string
	Timeout     time.Duration
	TokenSource TokenSource
}

// Client wraps an *http.Client with the shared transport + auth.
type Client struct {
	baseURL  *url.URL
	http     *http.Client
	tokenSrc TokenSource
}

// New constructs a Client. Returns an error if BaseURL doesn't parse.
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &Client{
		baseURL: u,
		http: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		tokenSrc: cfg.TokenSource,
	}, nil
}

// HTTP returns the underlying *http.Client. Callers that need to issue
// requests outside the Bearer-injecting Do flow (e.g. SSE streams, raw
// presigned-URL fetches) use this.
func (c *Client) HTTP() *http.Client { return c.http }

// BaseURL returns the parsed base URL (clone-safe — callers may mutate
// the returned value's Path/RawQuery without affecting the client).
func (c *Client) BaseURL() url.URL { return *c.baseURL }

// Endpoint joins path onto the configured base URL, trimming any
// trailing slash on the base path.
func (c *Client) Endpoint(path string) string {
	u := *c.baseURL
	u.Path = trimSlash(u.Path) + path
	return u.String()
}

// EndpointWithQuery is Endpoint with URL-encoded query params.
func (c *Client) EndpointWithQuery(path string, q url.Values) string {
	u := *c.baseURL
	u.Path = trimSlash(u.Path) + path
	if q != nil {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// InjectAuth fetches a Bearer token from TokenSource and sets the
// Authorization header. No-op if no TokenSource is configured.
func (c *Client) InjectAuth(ctx context.Context, req *http.Request) error {
	if c.tokenSrc == nil {
		return nil
	}
	tok, err := c.tokenSrc.Token(ctx)
	if err != nil {
		return fmt.Errorf("acquire service token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

// Do injects auth then sends the request through the otelhttp transport.
// No retry — callers that need retries layer that on top.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if err := c.InjectAuth(req.Context(), req); err != nil {
		return nil, err
	}
	return c.http.Do(req)
}

// DrainAndClose is a small helper for the common defer pattern.
func DrainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

func trimSlash(s string) string {
	return strings.TrimRight(s, "/")
}
