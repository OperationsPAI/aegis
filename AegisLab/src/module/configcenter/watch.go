package configcenter

import (
	"context"
	"sync"

	"aegis/infra/etcd"

	"github.com/sirupsen/logrus"
)

// dispatchFn is the per-change callback the watcher invokes.
type dispatchFn func(ctx context.Context, fullKey string, value []byte, deleted bool)

// watcher fronts a single etcd Watch under the env prefix and fans
// out events to the Center's dispatch callback. Bindings and HTTP
// subscribers register through the Center, not directly here.
type watcher struct {
	gw       *etcd.Gateway
	prefix   string
	dispatch dispatchFn

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newWatcher(gw *etcd.Gateway, prefix string, fn dispatchFn) *watcher {
	return &watcher{gw: gw, prefix: prefix, dispatch: fn}
}

func (w *watcher) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	ch := w.gw.Watch(ctx, w.prefix, true)
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case resp, ok := <-ch:
				if !ok {
					logrus.Warn("configcenter: etcd watch channel closed")
					return
				}
				if resp.Err() != nil {
					logrus.WithError(resp.Err()).Warn("configcenter: etcd watch response error")
					continue
				}
				for _, ev := range resp.Events {
					key := string(ev.Kv.Key)
					deleted := ev.Type.String() == "DELETE"
					w.dispatch(ctx, key, ev.Kv.Value, deleted)
				}
			}
		}
	}()
	return nil
}

func (w *watcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}

// subscriberMux fans out Entry events to per-namespace subscriber
// channels. Used by the HTTP SSE handler.
type subscriberMux struct {
	mu      sync.Mutex
	byNS    map[string][]chan Entry
	closed  bool
}

func newSubscriberMux() *subscriberMux {
	return &subscriberMux{byNS: make(map[string][]chan Entry)}
}

func (s *subscriberMux) subscribe(ctx context.Context, namespace string) (<-chan Entry, func()) {
	ch := make(chan Entry, 16)
	s.mu.Lock()
	s.byNS[namespace] = append(s.byNS[namespace], ch)
	s.mu.Unlock()
	unsub := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		subs := s.byNS[namespace]
		for i, c := range subs {
			if c == ch {
				s.byNS[namespace] = append(subs[:i], subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
	go func() {
		<-ctx.Done()
		unsub()
	}()
	return ch, unsub
}

func (s *subscriberMux) publish(namespace string, e Entry) {
	s.mu.Lock()
	subs := append([]chan Entry{}, s.byNS[namespace]...)
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- e:
		default:
			// drop if subscriber can't keep up; SSE clients re-list on reconnect
		}
	}
}

func (s *subscriberMux) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for ns, subs := range s.byNS {
		for _, ch := range subs {
			close(ch)
		}
		delete(s.byNS, ns)
	}
}
