package configcenterclient

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aegis/crud/admin/configcenter"
	"aegis/platform/consts"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Watch streams every change in `namespace` as Entry events on the returned
// channel until ctx is cancelled or the returned cancel func is invoked.
// Unlike Bind, Watch does not require pre-declaring keys — newly created keys
// surface as events too. Use this for the etcd→viper bridge.
//
// The channel is closed when the watch terminates.
type Watcher interface {
	Watch(ctx context.Context, namespace string) (<-chan configcenter.Entry, func(), error)
}

// Watch on LocalClient piggy-backs on the Center's in-process PubSub. The
// monolith / configcenter binary uses this branch.
func (c *LocalClient) Watch(ctx context.Context, namespace string) (<-chan configcenter.Entry, func(), error) {
	ps, ok := c.center.(configcenter.PubSub)
	if !ok {
		ch := make(chan configcenter.Entry)
		close(ch)
		return ch, func() {}, nil
	}
	wctx, cancel := context.WithCancel(ctx)
	src, unsub := ps.Subscribe(wctx, namespace)
	out := make(chan configcenter.Entry, 32)
	go func() {
		defer close(out)
		defer unsub()
		for e := range src {
			select {
			case out <- e:
			case <-wctx.Done():
				return
			}
		}
	}()
	return out, cancel, nil
}

// Watch on RemoteClient opens its own SSE stream against aegis-configcenter.
// Separate from the per-binding runWatch loop so newly created keys still
// surface (runWatch only reloads keys with pre-existing bindings).
func (c *RemoteClient) Watch(ctx context.Context, namespace string) (<-chan configcenter.Entry, func(), error) {
	wctx, cancel := context.WithCancel(ctx)
	out := make(chan configcenter.Entry, 32)
	go func() {
		defer close(out)
		endpoint := c.core.Endpoint(consts.APIPathConfigPrefix + namespace + "/watch")
		sseClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
		backoff := time.Second
		for {
			if wctx.Err() != nil {
				return
			}
			req, err := http.NewRequestWithContext(wctx, http.MethodGet, endpoint, nil)
			if err != nil {
				return
			}
			_ = c.core.InjectAuth(wctx, req)
			req.Header.Set("Accept", "text/event-stream")
			resp, err := sseClient.Do(req)
			if err != nil {
				select {
				case <-time.After(backoff):
				case <-wctx.Done():
					return
				}
				continue
			}
			if resp.StatusCode != http.StatusOK {
				_ = resp.Body.Close()
				select {
				case <-time.After(backoff):
				case <-wctx.Done():
					return
				}
				continue
			}
			drainSSEToChannel(wctx, resp.Body, out)
			_ = resp.Body.Close()
		}
	}()
	return out, cancel, nil
}

func drainSSEToChannel(ctx context.Context, body io.Reader, out chan<- configcenter.Entry) {
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
		var e configcenter.Entry
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			continue
		}
		select {
		case out <- e:
		case <-ctx.Done():
			return
		}
	}
}
