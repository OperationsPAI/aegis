package notificationclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"aegis/clients/internal/httpclient"
	"aegis/platform/consts"
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
	core       *httpclient.Client
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
	core, err := httpclient.New(httpclient.Config{
		BaseURL:     cfg.BaseURL,
		Timeout:     cfg.Timeout,
		TokenSource: tokenSrc,
	})
	if err != nil {
		return nil, fmt.Errorf("notification client: %w", err)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 2
	}
	return &RemoteClient{core: core, timeout: cfg.Timeout, maxRetries: cfg.MaxRetries}, nil
}

func (c *RemoteClient) Publish(ctx context.Context, req PublishReq) (*PublishResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode publish request: %w", err)
	}
	endpoint := c.core.Endpoint(consts.APIPathEventsPublish)

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.core.Do(httpReq)
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

func isRetryable(err error) bool {
	// Transport errors (DNS, connection reset, EOF). Net/url errors
	// in Go don't expose a clean "retryable" predicate, so a coarse
	// "yes, retry, the upstream loop bounds attempts" suffices.
	return err != nil
}

var _ Client = (*RemoteClient)(nil)
