package blobclient

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
// Same pattern as notificationclient.TokenSource — app/blob wires a
// concrete implementation (typically a wrapper over ssoclient).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// RemoteClient POSTs producer requests to aegis-blob's HTTP surface.
type RemoteClient struct {
	baseURL    *url.URL
	http       *http.Client
	tokenSrc   TokenSource
	maxRetries int
}

type RemoteClientConfig struct {
	BaseURL    string
	Timeout    time.Duration
	MaxRetries int
}

func NewRemoteClient(cfg RemoteClientConfig, tokenSrc TokenSource) (*RemoteClient, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse blob base url: %w", err)
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
		maxRetries: cfg.MaxRetries,
	}, nil
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func (c *RemoteClient) endpoint(path string) string {
	u := *c.baseURL
	u.Path = trimSlash(u.Path) + path
	return u.String()
}

func (c *RemoteClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		var token string
		if c.tokenSrc != nil {
			t, err := c.tokenSrc.Token(ctx)
			if err != nil {
				return fmt.Errorf("acquire service token: %w", err)
			}
			token = t
		}
		req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), rdr)
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("blob service responded %d", resp.StatusCode)
			_ = resp.Body.Close()
			continue
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusNoContent {
			return nil
		}
		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("blob request failed (%d): %s", resp.StatusCode, string(b))
		}
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
		return nil
	}
	return fmt.Errorf("blob request failed after %d retries: %w", c.maxRetries, lastErr)
}

func (c *RemoteClient) PresignPut(ctx context.Context, bucket string, req PresignPutReq) (*PresignPutResult, error) {
	var out PresignPutResult
	if err := c.do(ctx, http.MethodPost, consts.APIPathBlobBuckets+url.PathEscape(bucket)+"/presign-put", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *RemoteClient) PresignGet(ctx context.Context, bucket, key string, req PresignGetReq) (*PresignedURL, error) {
	body := struct {
		Key string `json:"key"`
		PresignGetReq
	}{Key: key, PresignGetReq: req}
	var out PresignedURL
	if err := c.do(ctx, http.MethodPost, consts.APIPathBlobBuckets+url.PathEscape(bucket)+"/presign-get", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *RemoteClient) Stat(ctx context.Context, bucket, key string) (*ObjectMeta, error) {
	var out ObjectMeta
	path := consts.APIPathBlobBuckets + url.PathEscape(bucket) + "/objects/" + url.PathEscape(key)
	if err := c.do(ctx, http.MethodHead, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *RemoteClient) Delete(ctx context.Context, bucket, key string) error {
	path := consts.APIPathBlobBuckets + url.PathEscape(bucket) + "/objects/" + url.PathEscape(key)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// PutBytes for the remote client: presign-put, then PUT the bytes to
// the returned URL. Phase A keeps this simple; full multipart support
// lands when the s3 driver does.
func (c *RemoteClient) PutBytes(ctx context.Context, bucket string, body []byte, req PresignPutReq) (*ObjectMeta, error) {
	req.ContentLength = int64(len(body))
	pres, err := c.PresignPut(ctx, bucket, req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, pres.Presigned.Method, pres.Presigned.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range pres.Presigned.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upload failed (%d)", resp.StatusCode)
	}
	return &ObjectMeta{Key: pres.Key, Size: int64(len(body)), ContentType: req.ContentType, UpdatedAt: time.Now()}, nil
}

func (c *RemoteClient) GetBytes(ctx context.Context, bucket, key string) ([]byte, *ObjectMeta, error) {
	pres, err := c.PresignGet(ctx, bucket, key, PresignGetReq{})
	if err != nil {
		return nil, nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, pres.Method, pres.URL, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, nil, fmt.Errorf("download failed (%d)", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return body, &ObjectMeta{Key: key, Size: int64(len(body)), UpdatedAt: time.Now()}, nil
}

var _ Client = (*RemoteClient)(nil)
