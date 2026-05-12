package configcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"aegis/platform/config"
	"aegis/platform/etcd"

	"github.com/mitchellh/mapstructure"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// Center is the in-process configuration-center surface. The HTTP
// handler in this package is the only caller that issues Set/Delete
// from outside — see audit.go for the actor-bearing wrapper.
type Center interface {
	Bind(ctx context.Context, namespace, key string, out any, opts ...BindOpt) (Handle, error)
	Get(ctx context.Context, namespace, key string) (raw []byte, layer Layer, err error)
	Set(ctx context.Context, namespace, key string, value any) error
	Delete(ctx context.Context, namespace, key string) error
	List(ctx context.Context, namespace string) ([]Entry, error)
}

// Handle is what Bind returns. Hold onto it for the lifetime you want
// the binding to keep tracking changes; Close to release the watcher.
type Handle interface {
	Reload(ctx context.Context) error
	Subscribe(fn func()) (unsubscribe func())
	Close() error
}

// secretKey rejects key paths that look like they carry secrets.
// Matches RFC §"Open questions / Secrets bleed".
var secretKey = regexp.MustCompile(`(?i)(password|secret|key|token)`)

// defaultCenter is the in-process implementation. It owns the etcd
// watcher multiplexer and the binding registry.
type defaultCenter struct {
	gw  *etcd.Gateway
	env string

	mu       sync.RWMutex
	bindings map[string]*binding // by fullKey
	subs     *subscriberMux      // namespace -> []chan Entry
	watcher  *watcher
}

// New constructs the Center. The fx provider in module.go wraps this.
func New(gw *etcd.Gateway) (*defaultCenter, error) {
	env := config.GetString("env")
	if env == "" {
		env = "dev"
	}
	c := &defaultCenter{
		gw:       gw,
		env:      env,
		bindings: make(map[string]*binding),
		subs:     newSubscriberMux(),
	}
	c.watcher = newWatcher(gw, c.fullPrefix(), c.dispatch)
	return c, nil
}

// Start opens the multiplexed etcd watch. Called from the fx hook.
func (c *defaultCenter) Start(ctx context.Context) error {
	return c.watcher.Start(ctx)
}

// Stop closes the watcher and any open subscriber channels.
func (c *defaultCenter) Stop(_ context.Context) error {
	c.watcher.Stop()
	c.subs.closeAll()
	return nil
}

func (c *defaultCenter) fullPrefix() string {
	return fmt.Sprintf("/aegis/%s/", c.env)
}

func (c *defaultCenter) fullKey(namespace, key string) string {
	return fmt.Sprintf("/aegis/%s/%s/%s", c.env, namespace, key)
}

func splitKey(env, full string) (namespace, key string, ok bool) {
	prefix := fmt.Sprintf("/aegis/%s/", env)
	if !strings.HasPrefix(full, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(full, prefix)
	idx := strings.IndexByte(rest, '/')
	if idx <= 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// resolveLayered runs the merge: etcd > env > toml > default.
// Returns raw JSON bytes plus which layer produced them.
func (c *defaultCenter) resolveLayered(ctx context.Context, namespace, key string, def any) ([]byte, Layer, error) {
	// etcd
	if raw, err := c.gw.Get(ctx, c.fullKey(namespace, key)); err == nil && raw != "" {
		return []byte(raw), LayerEtcd, nil
	}
	viperKey := namespace + "." + key
	envName := strings.ToUpper(strings.NewReplacer(".", "_").Replace(viperKey))
	if v, ok := os.LookupEnv(envName); ok && v != "" {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrEncode, err)
		}
		return b, LayerEnv, nil
	}
	if viper.IsSet(viperKey) {
		v := viper.Get(viperKey)
		b, err := json.Marshal(v)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrEncode, err)
		}
		return b, LayerTOML, nil
	}
	if def != nil {
		b, err := json.Marshal(def)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrEncode, err)
		}
		return b, LayerDefault, nil
	}
	return nil, "", ErrNotFound
}

// Bind decodes the current resolved value into out and registers a
// watcher so subsequent etcd changes update out atomically.
func (c *defaultCenter) Bind(ctx context.Context, namespace, key string, out any, opts ...BindOpt) (Handle, error) {
	bopts := bindOptions{}
	for _, o := range opts {
		o(&bopts)
	}
	outV := reflect.ValueOf(out)
	if outV.Kind() != reflect.Pointer || outV.IsNil() {
		return nil, fmt.Errorf("configcenter.Bind: out must be a non-nil pointer")
	}

	b := &binding{
		namespace: namespace,
		key:       key,
		fullKey:   c.fullKey(namespace, key),
		out:       outV,
		opts:      bopts,
	}
	if err := b.reload(ctx, c); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.bindings[b.fullKey] = b
	c.mu.Unlock()
	return b, nil
}

// Get returns the resolved raw bytes and resolving layer.
func (c *defaultCenter) Get(ctx context.Context, namespace, key string) ([]byte, Layer, error) {
	return c.resolveLayered(ctx, namespace, key, nil)
}

// Set writes a value to the etcd layer. Always JSON-encodes value.
func (c *defaultCenter) Set(ctx context.Context, namespace, key string, value any) error {
	if secretKey.MatchString(key) || secretKey.MatchString(namespace) {
		return ErrForbiddenKey
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
		if !json.Valid(raw) {
			b, err := json.Marshal(string(raw))
			if err != nil {
				return fmt.Errorf("%w: %v", ErrEncode, err)
			}
			raw = b
		}
	case json.RawMessage:
		raw = v
	default:
		b, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrEncode, err)
		}
		raw = b
	}
	return c.gw.Put(ctx, c.fullKey(namespace, key), string(raw), 0)
}

// Delete removes the etcd-layer entry; lower layers reappear.
func (c *defaultCenter) Delete(ctx context.Context, namespace, key string) error {
	return c.gw.Delete(ctx, c.fullKey(namespace, key))
}

// List returns every key under a namespace with current value + layer.
//
// v1 lists only the etcd layer (lower layers are baked into the
// process and not enumerable cheaply). Lower layers still resolve via
// Get on demand.
func (c *defaultCenter) List(ctx context.Context, namespace string) ([]Entry, error) {
	prefix := fmt.Sprintf("/aegis/%s/%s/", c.env, namespace)
	kvs, err := c.gw.ListPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(kvs))
	for _, kv := range kvs {
		key := strings.TrimPrefix(kv.Key, prefix)
		var val any
		if jerr := json.Unmarshal([]byte(kv.Value), &val); jerr != nil {
			val = kv.Value
		}
		out = append(out, Entry{
			Namespace: namespace,
			Key:       key,
			Value:     val,
			Layer:     LayerEtcd,
		})
	}
	return out, nil
}

// dispatch is the callback the watcher fires on every etcd change.
// It re-decodes affected bindings and pushes an Entry to subscribers.
func (c *defaultCenter) dispatch(ctx context.Context, fullKey string, value []byte, deleted bool) {
	namespace, key, ok := splitKey(c.env, fullKey)
	if !ok {
		return
	}

	c.mu.RLock()
	b := c.bindings[fullKey]
	c.mu.RUnlock()
	if b != nil {
		if err := b.reload(ctx, c); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"namespace": namespace, "key": key,
			}).Warn("configcenter: bind reload failed; keeping previous value")
		}
	}

	var entryVal any
	layer := LayerEtcd
	if deleted {
		layer = LayerDefault
		entryVal = nil
	} else if jerr := json.Unmarshal(value, &entryVal); jerr != nil {
		entryVal = string(value)
	}
	c.subs.publish(namespace, Entry{
		Namespace: namespace,
		Key:       key,
		Value:     entryVal,
		Layer:     layer,
	})
}

// Subscribe gives the HTTP SSE handler (and others) a way to receive
// every change under a namespace.
func (c *defaultCenter) Subscribe(ctx context.Context, namespace string) (<-chan Entry, func()) {
	return c.subs.subscribe(ctx, namespace)
}

// --- binding ---

type binding struct {
	namespace string
	key       string
	fullKey   string

	mu          sync.Mutex
	out         reflect.Value // pointer
	last        reflect.Value // last successful resolved value (for rollback)
	opts        bindOptions
	subscribers []func()
	closed      bool
}

func (b *binding) reload(ctx context.Context, c *defaultCenter) error {
	raw, _, err := c.resolveLayered(ctx, b.namespace, b.key, b.opts.defaultValue)
	if err != nil {
		return err
	}
	target := reflect.New(b.out.Elem().Type())
	dec, derr := newMapstructureDecoder(target.Interface())
	if derr != nil {
		return derr
	}

	var jsonVal any
	if jerr := json.Unmarshal(raw, &jsonVal); jerr != nil {
		return fmt.Errorf("%w: %v", ErrDecode, jerr)
	}
	if err := dec.Decode(jsonVal); err != nil {
		return fmt.Errorf("%w: %v", ErrDecode, err)
	}

	candidate := target.Elem()
	if b.opts.validator != nil {
		if verr := b.opts.validator(candidate.Interface()); verr != nil {
			return fmt.Errorf("%w: %v", ErrInvalidValue, verr)
		}
	}
	b.mu.Lock()
	b.out.Elem().Set(candidate)
	b.last = candidate
	subs := append([]func(){}, b.subscribers...)
	b.mu.Unlock()
	for _, fn := range subs {
		fn()
	}
	return nil
}

func newMapstructureDecoder(target any) (*mapstructure.Decoder, error) {
	return mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           target,
		TagName:          "mapstructure",
		WeaklyTypedInput: true,
	})
}

func (b *binding) Reload(ctx context.Context) error {
	// The handle's parent Center is reachable through the registry —
	// but since reload is centred on Center.resolveLayered, we rely on
	// the watcher path. Reload triggers a one-shot re-read by reusing
	// dispatch via a direct ListPrefix is overkill; instead expose
	// Reload as "force-refresh against current sources".
	return b.reloadVia(ctx)
}

// reloadVia is a thin shim so Reload doesn't require holding the
// Center pointer; for v1 we simply re-bind via the package singleton
// captured at construction. To keep the binding self-sufficient
// without leaking a back-pointer cycle, we capture Center on the
// binding lazily.
//
// TODO: thread Center pointer onto binding once we drop the package
// singleton — kept minimal for the skeleton.
var globalCenter *defaultCenter

func (b *binding) reloadVia(ctx context.Context) error {
	if globalCenter == nil {
		return fmt.Errorf("configcenter: no center registered")
	}
	return b.reload(ctx, globalCenter)
}

func (b *binding) Subscribe(fn func()) func() {
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

func (b *binding) Close() error {
	b.mu.Lock()
	b.closed = true
	b.subscribers = nil
	b.mu.Unlock()
	return nil
}
