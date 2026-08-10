package blobclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aegis/clients/internal/httpclient"
	"aegis/platform/consts"
)

// TokenSource produces a fresh Bearer token for cross-service calls.
// Same pattern as notificationclient.TokenSource — app/blob wires a
// concrete implementation (typically a wrapper over ssoclient).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// RemoteClient POSTs producer requests to aegis-blob's HTTP surface.
type RemoteClient struct {
	core       *httpclient.Client
	maxRetries int
}

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
		return nil, fmt.Errorf("blob client: %w", err)
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 2
	}
	return &RemoteClient{core: core, maxRetries: cfg.MaxRetries}, nil
}

// escapeKeyPath percent-escapes each segment of key but leaves the
// "/" separators alone so the result still matches a wildcard route.
func escapeKeyPath(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// doWithQuery mirrors do() but lets callers pass URL query params
// without smuggling "?..." through the path.
func (c *RemoteClient) doWithQuery(ctx context.Context, method, path string, q url.Values, body any, out any) error {
	return c.execute(ctx, method, c.core.EndpointWithQuery(path, q), body, out)
}

func (c *RemoteClient) do(ctx context.Context, method, path string, body any, out any) error {
	return c.execute(ctx, method, c.core.Endpoint(path), body, out)
}

func (c *RemoteClient) execute(ctx context.Context, method, target string, body, out any) error {
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		raw = b
	}
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		var reader io.Reader
		if raw != nil {
			reader = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, reader)
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.core.Do(req)
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
	resp, err := c.core.HTTP().Do(httpReq)
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
	resp, err := c.core.HTTP().Do(httpReq)
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

// List GETs /blob/buckets/{bucket}/object-list with S3-style query
// params and decodes the driver-shaped response into the client
// ListResult shape.
func (c *RemoteClient) List(ctx context.Context, bucket, prefix string, opts ListOpts) (*ListResult, error) {
	q := url.Values{}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if opts.ContinuationToken != "" {
		q.Set("continuation_token", opts.ContinuationToken)
	}
	if opts.Delimiter != "" {
		q.Set("delimiter", opts.Delimiter)
	}
	if opts.MaxKeys > 0 {
		q.Set("max_keys", fmt.Sprintf("%d", opts.MaxKeys))
	}
	path := consts.APIPathBlobBuckets + url.PathEscape(bucket) + "/object-list"
	// Server returns blob.ListResult ({items, common_prefixes,
	// next_continuation_token, is_truncated}). Decode into a wire
	// struct and translate to the client shape (items → objects).
	var wire struct {
		Items                 []ObjectMeta `json:"items"`
		CommonPrefixes        []string     `json:"common_prefixes"`
		NextContinuationToken string       `json:"next_continuation_token"`
		IsTruncated           bool         `json:"is_truncated"`
	}
	if err := c.doWithQuery(ctx, http.MethodGet, path, q, nil, &wire); err != nil {
		return nil, err
	}
	return &ListResult{
		Objects:               wire.Items,
		CommonPrefixes:        wire.CommonPrefixes,
		NextContinuationToken: wire.NextContinuationToken,
		IsTruncated:           wire.IsTruncated,
	}, nil
}

// GetReader streams the object bytes from the InlineGet endpoint. The
// response body is NOT read fully — callers must Close the returned
// reader to release the connection. Meta is built from response
// headers (Content-Type / Content-Length).
func (c *RemoteClient) GetReader(ctx context.Context, bucket, key string) (io.ReadCloser, *ObjectMeta, error) {
	// /stream/*key route accepts keys-with-slashes. Don't percent-
	// escape the slashes themselves — the route is a wildcard.
	path := consts.APIPathBlobBuckets + url.PathEscape(bucket) + "/stream/" + escapeKeyPath(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.core.Endpoint(path), nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.core.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, nil, fmt.Errorf("blob get-reader: object not found")
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, nil, fmt.Errorf("blob get-reader failed (%d): %s", resp.StatusCode, string(b))
	}
	meta := &ObjectMeta{
		Key:         key,
		ContentType: resp.Header.Get("Content-Type"),
		UpdatedAt:   time.Now(),
	}
	if resp.ContentLength > 0 {
		meta.Size = resp.ContentLength
	}
	return resp.Body, meta, nil
}

var _ Client = (*RemoteClient)(nil)
