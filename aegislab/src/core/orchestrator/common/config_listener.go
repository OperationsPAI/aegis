package common

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
	etcd "aegis/platform/etcd"

	"github.com/sirupsen/logrus"
	clientv3 "go.etcd.io/etcd/client/v3"
	"gorm.io/gorm"
)

// scopePrefix maps configuration scopes to their etcd key prefix.
// Scopes absent from the map (e.g. producer) have no etcd representation.
var scopePrefix = map[consts.ConfigScope]string{
	consts.ConfigScopeProducer: consts.ConfigEtcdProducerPrefix,
	consts.ConfigScopeConsumer: consts.ConfigEtcdConsumerPrefix,
	consts.ConfigScopeGlobal:   consts.ConfigEtcdGlobalPrefix,
}

// configUpdateListener listens for configuration update events from etcd.
// It supports incremental scope activation via EnsureScope — each scope is
// loaded and watched independently, making it safe for both, producer-only
// and consumer-only modes.
type ConfigUpdateListener struct {
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	active       map[consts.ConfigScope]bool // scopes already loaded + watched
	bridgeActive bool                        // configcenter-tree bridge watcher started
	db           *gorm.DB
	gateway      *etcd.Gateway
}

func NewConfigUpdateListener(ctx context.Context, db *gorm.DB, gateway *etcd.Gateway) *ConfigUpdateListener {
	listenerCtx, cancel := context.WithCancel(ctx)
	listener := &ConfigUpdateListener{
		ctx:     listenerCtx,
		cancel:  cancel,
		active:  make(map[consts.ConfigScope]bool),
		db:      db,
		gateway: gateway,
	}

	go func() {
		<-ctx.Done()
		logrus.Info("Parent context cancelled, stopping config update listener...")
		listener.Stop()
	}()

	return listener
}

// EnsureScope loads initial config values from etcd and starts a watcher for
// the given scope. The call is idempotent — invoking it multiple times for the
// same scope is a safe no-op. Scopes without an etcd prefix (e.g. producer)
// are silently skipped.
func (l *ConfigUpdateListener) EnsureScope(scope consts.ConfigScope) error {
	prefix, ok := scopePrefix[scope]
	if !ok {
		logrus.Debugf("Scope %s has no etcd prefix, skipping listener setup",
			consts.GetConfigScopeName(scope))
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.active[scope] {
		return nil
	}

	scopeName := consts.GetConfigScopeName(scope)

	// Load initial values from etcd into viper
	if err := l.loadScopeFromEtcd(scope, prefix, scopeName); err != nil {
		return fmt.Errorf("failed to load %s configs from etcd: %w", scopeName, err)
	}

	// Start a dedicated watcher goroutine for this scope
	go l.watchPrefix(prefix, scopeName)

	l.active[scope] = true
	logrus.Infof("Config listener active for scope %s (prefix=%s)", scopeName, prefix)
	return nil
}

// configCenterPrefix returns the etcd prefix the aegis-configcenter service
// (and `aegisctl etcd put`) writes to: /aegis/<env>/. It mirrors
// crud/admin/configcenter.defaultCenter.fullPrefix without importing that
// standalone package.
func configCenterPrefix() string {
	env := config.GetString("env")
	if env == "" {
		env = "dev"
	}
	return fmt.Sprintf("/aegis/%s/", env)
}

// EnsureConfigCenterBridge starts a watcher on the aegis-configcenter etcd
// tree (/aegis/<env>/<namespace>/<key>). `aegisctl etcd put` writes there,
// which is a different tree from the scope-prefixed /rcabench/config/ tree
// loaded by EnsureScope — without this bridge an operator's etcd put never
// reaches the registered consumer handlers (e.g. the rate limiters). Each
// event's namespace+key is rejoined into the dotted dynamic_config key and
// routed through the same handler registry as the native tree.
func (l *ConfigUpdateListener) EnsureConfigCenterBridge() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.bridgeActive {
		return
	}
	prefix := configCenterPrefix()
	go l.watchConfigCenter(prefix)
	l.bridgeActive = true
	logrus.Infof("Config listener bridge active for configcenter tree (prefix=%s)", prefix)
}

// Stop cancels the listener context, stopping all watcher goroutines.
func (l *ConfigUpdateListener) Stop() {
	l.cancel()
	logrus.Info("Config update listener stopped")
}

// loadScopeFromEtcd loads all configs for a given scope from etcd into viper.
// Falls back to MySQL defaults only if config doesn't exist in etcd.
func (l *ConfigUpdateListener) loadScopeFromEtcd(scope consts.ConfigScope, prefix, scopeName string) error {
	configMetadata, err := newConfigStore(l.db).listConfigsByScope(scope)
	if err != nil {
		return fmt.Errorf("failed to list %s config metadata from database: %w", scopeName, err)
	}

	loadedCount := 0
	initializedCount := 0

	for _, meta := range configMetadata {
		etcdKey := fmt.Sprintf("%s%s", prefix, meta.Key)

		// Try to get current value from etcd first
		etcdValue, err := l.gateway.Get(l.ctx, etcdKey)
		if err != nil {
			logrus.Errorf("Failed to get config %s from etcd: %v", meta.Key, err)
			continue
		}

		var valueToLoad string
		if etcdValue == "" {
			// Config doesn't exist in etcd, initialize it with MySQL default value
			if err := l.gateway.Put(l.ctx, etcdKey, meta.DefaultValue, 0); err != nil {
				logrus.Errorf("Failed to initialize config %s in etcd: %v", meta.Key, err)
				continue
			}

			valueToLoad = meta.DefaultValue
			initializedCount++
			logrus.Infof("Initialized config %s in etcd with default value from MySQL", meta.Key)
		} else {
			valueToLoad = etcdValue
		}

		// Load config to Viper (local memory cache)
		if err := config.SetViperValue(meta.Key, valueToLoad, meta.ValueType); err != nil {
			logrus.Errorf("Failed to load config %s to Viper: %v", meta.Key, err)
			continue
		}
		loadedCount++
	}

	logrus.Infof("Loaded %d/%d %s configs from etcd to Viper (initialized %d new configs)",
		loadedCount, len(configMetadata), scopeName, initializedCount)

	return nil
}

// watchPrefix watches a single etcd prefix for configuration changes.
// Each scope gets its own goroutine calling this method.
func (l *ConfigUpdateListener) watchPrefix(prefix, scopeName string) {
	watchChan := l.gateway.Watch(l.ctx, prefix, true)
	logrus.Infof("Started watching etcd prefix %s for %s config changes", prefix, scopeName)

	for {
		select {
		case <-l.ctx.Done():
			logrus.Infof("Config watcher for %s stopped (context cancelled)", scopeName)
			return

		case watchResp, ok := <-watchChan:
			if !ok {
				logrus.Warnf("etcd %s watch channel closed, restarting...", scopeName)
				time.Sleep(1 * time.Second)
				watchChan = l.gateway.Watch(l.ctx, prefix, true)
				continue
			}
			if watchResp.Canceled {
				logrus.Warnf("etcd %s watch was canceled, restarting...", scopeName)
				time.Sleep(1 * time.Second)
				watchChan = l.gateway.Watch(l.ctx, prefix, true)
				continue
			}
			if err := watchResp.Err(); err != nil {
				logrus.Errorf("etcd %s watch error: %v", scopeName, err)
				time.Sleep(1 * time.Second)
				watchChan = l.gateway.Watch(l.ctx, prefix, true)
				continue
			}
			for _, event := range watchResp.Events {
				l.handleEtcdEvent(event, prefix)
			}
		}
	}
}

// handleEtcdEvent handles a single etcd event from a given prefix
func (l *ConfigUpdateListener) handleEtcdEvent(event *clientv3.Event, prefix string) {
	key := string(event.Kv.Key)
	newValue := string(event.Kv.Value)

	// Extract config key (remove prefix)
	if len(key) <= len(prefix) {
		logrus.Warnf("Invalid etcd key: %s", key)
		return
	}
	configKey := key[len(prefix):]

	var oldValue string
	if event.PrevKv != nil {
		oldValue = string(event.PrevKv.Value)
	}

	logrus.WithFields(logrus.Fields{
		"type":      event.Type,
		"key":       configKey,
		"old_value": oldValue,
		"new_value": newValue,
	}).Info("received config change from etcd")

	// Apply config change via registry
	if err := handleConfigChange(l.ctx, l.db, configKey, oldValue, newValue); err != nil {
		logrus.Errorf("failed to apply config update for %s: %v", configKey, err)
		return
	}

	logrus.Infof("successfully applied config change for %s", configKey)
}

// watchConfigCenter watches the configcenter tree (/aegis/<env>/) and routes
// changes whose dotted key maps to a known dynamic_config row through the
// handler registry. Same restart semantics as watchPrefix.
func (l *ConfigUpdateListener) watchConfigCenter(prefix string) {
	watchChan := l.gateway.Watch(l.ctx, prefix, true)
	logrus.Infof("Started watching etcd prefix %s for configcenter config changes", prefix)

	for {
		select {
		case <-l.ctx.Done():
			logrus.Info("Config watcher for configcenter stopped (context cancelled)")
			return

		case watchResp, ok := <-watchChan:
			if !ok {
				logrus.Warn("etcd configcenter watch channel closed, restarting...")
				time.Sleep(1 * time.Second)
				watchChan = l.gateway.Watch(l.ctx, prefix, true)
				continue
			}
			if watchResp.Canceled {
				logrus.Warn("etcd configcenter watch was canceled, restarting...")
				time.Sleep(1 * time.Second)
				watchChan = l.gateway.Watch(l.ctx, prefix, true)
				continue
			}
			if err := watchResp.Err(); err != nil {
				logrus.Errorf("etcd configcenter watch error: %v", err)
				time.Sleep(1 * time.Second)
				watchChan = l.gateway.Watch(l.ctx, prefix, true)
				continue
			}
			for _, event := range watchResp.Events {
				l.handleConfigCenterEvent(event, prefix)
			}
		}
	}
}

// handleConfigCenterEvent translates a configcenter etcd event into a
// dynamic_config update. The configcenter stores keys as
// /aegis/<env>/<namespace>/<key> with JSON-encoded values; the dotted config
// key is <namespace>.<key>. Keys without a matching dynamic_config row (most
// of the configcenter namespace) are silently skipped — handleConfigChange's
// getConfigByKey lookup is the gate.
func (l *ConfigUpdateListener) handleConfigCenterEvent(event *clientv3.Event, prefix string) {
	configKey, newValue, ok := parseConfigCenterKV(prefix, string(event.Kv.Key), event.Kv.Value)
	if !ok {
		return
	}
	var oldValue string
	if event.PrevKv != nil {
		oldValue = decodeConfigCenterValue(event.PrevKv.Value)
	}

	cfg, err := newConfigStore(l.db).getConfigByKey(configKey)
	if err != nil {
		logrus.Debugf("configcenter key %s has no dynamic_config row, skipping bridge", configKey)
		return
	}
	if scopePrefix[cfg.Scope] == "" {
		return
	}

	logrus.WithFields(logrus.Fields{
		"type":      event.Type,
		"key":       configKey,
		"old_value": oldValue,
		"new_value": newValue,
		"source":    "configcenter",
	}).Info("received config change from etcd")

	if err := handleConfigChange(l.ctx, l.db, configKey, oldValue, newValue); err != nil {
		logrus.Errorf("failed to apply configcenter config update for %s: %v", configKey, err)
		return
	}
	logrus.Infof("successfully applied config change for %s", configKey)
}

// parseConfigCenterKV reconstructs the dotted dynamic_config key and the plain
// string value from a configcenter etcd entry (/aegis/<env>/<ns>/<key> with a
// JSON-encoded value). ok is false for keys outside the prefix or without a
// namespace/key split.
func parseConfigCenterKV(prefix, fullKey string, rawValue []byte) (configKey, value string, ok bool) {
	if len(fullKey) <= len(prefix) || !strings.HasPrefix(fullKey, prefix) {
		return "", "", false
	}
	rest := fullKey[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash == len(rest)-1 {
		return "", "", false
	}
	return rest[:slash] + "." + rest[slash+1:], decodeConfigCenterValue(rawValue), true
}

// decodeConfigCenterValue unwraps a JSON-encoded scalar (the form
// `aegisctl etcd put` writes) into the plain string SetViperValue expects.
// Non-JSON or non-scalar payloads are passed through verbatim.
func decodeConfigCenterValue(raw []byte) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return string(raw)
}
