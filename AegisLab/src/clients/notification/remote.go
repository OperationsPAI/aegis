package notificationclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"aegis/platform/consts"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// TokenSource produces a fresh Bearer token for cross-service calls.
// The app wiring layer (app/...) supplies an adapter — typically a
// thin wrapper over ssoclient that requests `client_credentials`
// against the SSO and caches the result. Held as an interface so the
// remote client stays independent of ssoclient.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// RemoteClient POSTs PublishReq as JSON to the standalone notification
// service at `${base_url}/api/v2/events:publish`. Service-to-service
// auth uses the SSO client_credentials grant (same pattern as
// aegis-backend authenticates against SSO today) — the ssoclient
// transparently injects a Bearer token.
//
// The struct + interface let producers compile against this package
// without knowing whether their binary will run mono or split.
type RemoteClient struct {
	baseURL    *url.URL
	http       *http.Client
	tokenSrc   TokenSource
	timeout    time.Duration
	maxRetries int
}

// RemoteClientConfig wires the remote client; values typically come
// from `[notification.remote]` config.
type RemoteClientConfig struct {
	BaseURL    string
	Timeout    time.Duration
	MaxRetries int
}

func NewRemoteClient(cfg RemoteClientConfig, tokenSrc TokenSource) (*RemoteClient, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse notification base url: %w", err)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 2
	}
	return &RemoteClient{
		baseURL:    u,
		http: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		tokenSrc:   tokenSrc,
		timeout:    cfg.Timeout,
		maxRetries: cfg.MaxRetries,
	}, nil
}

func (c *RemoteClient) Publish(ctx context.Context, req PublishReq) (*PublishResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode publish request: %w", err)
	}
	endpoint := *c.baseURL
	endpoint.Path = trimSlash(endpoint.Path) + consts.APIPathEventsPublish

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		token, err := c.tokenSrc.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("acquire service token: %w", err)
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+token)

		resp, err := c.http.Do(httpReq)
		if err != nil {
			lastErr = err
			if !isRetryable(err) {
				return nil, err
			}
			continue
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("notification service responded %d", resp.StatusCode)
			_ = resp.Body.Close()
			continue
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("publish failed (%d): %s", resp.StatusCode, string(b))
		}
		var out PublishResult
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, fmt.Errorf("decode publish response: %w", err)
		}
		return &out, nil
	}
	return nil, fmt.Errorf("publish failed after %d retries: %w", c.maxRetries, lastErr)
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func isRetryable(err error) bool {
	// Transport errors (DNS, connection reset, EOF). Net/url errors
	// in Go don't expose a clean "retryable" predicate, so a coarse
	// "yes, retry, the upstream loop bounds attempts" suffices.
	return err != nil
}

var _ Client = (*RemoteClient)(nil)
