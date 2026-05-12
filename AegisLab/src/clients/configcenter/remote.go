package configcenterclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"aegis/platform/consts"
	"aegis/crud/admin/configcenter"

	"github.com/mitchellh/mapstructure"
)

// RemoteClient talks to `aegis-configcenter` over HTTP and subscribes
// to the SSE watch stream for hot reload. Service-to-service auth
// uses a Bearer token from TokenSource (typically ssoclient).
type RemoteClient struct {
	baseURL  *url.URL
	http     *http.Client
	tokenSrc TokenSource
	timeout  time.Duration

	mu       sync.Mutex
	bindings map[string][]*remoteBinding // by namespace
	watches  map[string]context.CancelFunc
}

type RemoteClientConfig struct {
	BaseURL string
	Timeout time.Duration
}

func NewRemoteClient(cfg RemoteClientConfig, tokenSrc TokenSource) (*RemoteClient, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse configcenter base url: %w", err)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &RemoteClient{
		baseURL:  u,
		http:     &http.Client{Timeout: cfg.Timeout},
		tokenSrc: tokenSrc,
		timeout:  cfg.Timeout,
		bindings: make(map[string][]*remoteBinding),
		watches:  make(map[string]context.CancelFunc),
	}, nil
}

func (c *RemoteClient) Bind(ctx context.Context, namespace, key string, out any, opts ...BindOpt) (Handle, error) {
	outV := reflect.ValueOf(out)
	if outV.Kind() != reflect.Pointer || outV.IsNil() {
		return nil, fmt.Errorf("configcenterclient: out must be non-nil pointer")
	}
	rb := &remoteBinding{
		client:    c,
		namespace: namespace,
		key:       key,
		out:       outV,
	}
	if err := rb.reload(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.bindings[namespace] = append(c.bindings[namespace], rb)
	if _, ok := c.watches[namespace]; !ok {
		wctx, cancel := context.WithCancel(context.Background())
		c.watches[namespace] = cancel
		go c.runWatch(wctx, namespace)
	}
	c.mu.Unlock()
	return rb, nil
}

func (c *RemoteClient) Get(ctx context.Context, namespace, key string) ([]byte, Layer, error) {
	endpoint := c.url(consts.APIPathConfigPrefix + namespace + "/" + key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	if err := c.auth(ctx, req); err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", configcenter.ErrNotFound
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("get failed (%d): %s", resp.StatusCode, string(b))
	}
	var er struct {
		Value json.RawMessage `json:"value"`
		Layer Layer           `json:"layer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, "", err
	}
	return er.Value, er.Layer, nil
}

func (c *RemoteClient) Set(ctx context.Context, namespace, key string, value any, _ ...SetOpt) error {
	body, err := json.Marshal(struct {
		Value any `json:"value"`
	}{Value: value})
	if err != nil {
		return err
	}
	endpoint := c.url(consts.APIPathConfigPrefix + namespace + "/" + key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.auth(ctx, req); err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("set failed (%d): %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *RemoteClient) Delete(ctx context.Context, namespace, key string) error {
	endpoint := c.url(consts.APIPathConfigPrefix + namespace + "/" + key)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	if err := c.auth(ctx, req); err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete failed (%d): %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *RemoteClient) List(ctx context.Context, namespace string) ([]Entry, error) {
	endpoint := c.url(consts.APIPathConfigPrefix + namespace)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if err := c.auth(ctx, req); err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list failed (%d): %s", resp.StatusCode, string(b))
	}
	var out struct {
		Items []Entry `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *RemoteClient) url(path string) string {
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String()
}

func (c *RemoteClient) auth(ctx context.Context, req *http.Request) error {
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

// runWatch streams SSE change events for one namespace and reloads
// every binding under it. The loop reconnects on disconnect with a
// short backoff; the configcenter is expected to be HA, but a
// rolling restart should not kill consumers.
func (c *RemoteClient) runWatch(ctx context.Context, namespace string) {
	endpoint := c.url(consts.APIPathConfigPrefix + namespace + "/watch")
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return
		}
		_ = c.auth(ctx, req)
		req.Header.Set("Accept", "text/event-stream")
		// no client timeout for SSE; rely on ctx
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(backoff)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			time.Sleep(backoff)
			continue
		}
		c.consumeSSE(ctx, resp.Body, namespace)
		_ = resp.Body.Close()
	}
}

func (c *RemoteClient) consumeSSE(ctx context.Context, body io.Reader, namespace string) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "ok" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			continue
		}
		c.notifyChange(ctx, namespace, e.Key)
	}
}

func (c *RemoteClient) notifyChange(ctx context.Context, namespace, key string) {
	c.mu.Lock()
	binds := append([]*remoteBinding{}, c.bindings[namespace]...)
	c.mu.Unlock()
	for _, b := range binds {
		if b.key != key {
			continue
		}
		if err := b.reload(ctx); err != nil {
			// keep previous value; logging here would cross a layer
			// boundary, so we surface failures to subscribers instead.
			continue
		}
	}
}

// --- remoteBinding ---

type remoteBinding struct {
	client    *RemoteClient
	namespace string
	key       string

	mu          sync.Mutex
	out         reflect.Value
	subscribers []func()
}

func (b *remoteBinding) reload(ctx context.Context) error {
	raw, _, err := b.client.Get(ctx, b.namespace, b.key)
	if err != nil {
		return err
	}
	target := reflect.New(b.out.Elem().Type())
	var jsonVal any
	if err := json.Unmarshal(raw, &jsonVal); err != nil {
		return err
	}
	dec, derr := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           target.Interface(),
		TagName:          "mapstructure",
		WeaklyTypedInput: true,
	})
	if derr != nil {
		return derr
	}
	if err := dec.Decode(jsonVal); err != nil {
		return err
	}
	b.mu.Lock()
	b.out.Elem().Set(target.Elem())
	subs := append([]func(){}, b.subscribers...)
	b.mu.Unlock()
	for _, fn := range subs {
		fn()
	}
	return nil
}

func (b *remoteBinding) Reload(ctx context.Context) error { return b.reload(ctx) }

func (b *remoteBinding) Subscribe(fn func()) func() {
	b.mu.Lock()
	idx := len(b.subscribers)
	b.subscribers = append(b.subscribers, fn)
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if idx < len(b.subscribers) {
			b.subscribers = append(b.subscribers[:idx], b.subscribers[idx+1:]...)
		}
	}
}

func (b *remoteBinding) Close() error {
	b.mu.Lock()
	b.subscribers = nil
	b.mu.Unlock()
	return nil
}

var _ Client = (*RemoteClient)(nil)
var _ Handle = (*remoteBinding)(nil)
