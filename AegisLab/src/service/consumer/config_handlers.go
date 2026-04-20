package consumer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aegis/config"
	"aegis/consts"
	k8s "aegis/infra/k8s"
	"aegis/service/common"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/sirupsen/logrus"
)

// RegisterConsumerHandlers registers the configuration handlers that the
// consumer process owns. The chaos-system handler covers cross-process data
// (producer validates against it, consumer applies it) and is therefore
// registered under Global scope; everything else is consumer-local.
//
// Should be called during consumer initialization, after RegisterGlobalHandlers.
func RegisterConsumerHandlers(
	controller *k8s.Controller,
	monitor NamespaceMonitor,
	publisher common.ConfigPublisher,
	restartLimiter *TokenBucketRateLimiter,
	buildLimiter *TokenBucketRateLimiter,
	algoLimiter *TokenBucketRateLimiter,
) {
	h := newChaosSystemHandler(monitor, controller, publisher)
	for _, category := range chaosSystemCategories() {
		common.RegisterHandler(h.forCategory(category))
	}
	common.RegisterHandler(newRateLimitingConfigHandler(
		publisher,
		restartLimiter,
		buildLimiter,
		algoLimiter,
	))
	consumerScope := consts.ConfigScopeConsumer
	globalScope := consts.ConfigScopeGlobal
	logrus.Infof("Registered consumer config handlers: consumer=%v global=%v",
		common.ListRegisteredConfigKeys(&consumerScope),
		common.ListRegisteredConfigKeys(&globalScope))
}

// UpdateK8sController updates K8s controller informers based on namespace changes.
func UpdateK8sController(controller *k8s.Controller, toAdd, toRemove []string) error {
	if controller == nil {
		logrus.Warn("Controller not initialized, skipping informer update")
		return nil
	}

	if len(toAdd) > 0 {
		logrus.Infof("Adding informers for active namespaces: %v", toAdd)
		if err := controller.AddNamespaceInformers(toAdd); err != nil {
			return fmt.Errorf("failed to add namespace informers: %w", err)
		}
	}

	if len(toRemove) > 0 {
		logrus.Infof("Marking namespaces as inactive: %v", toRemove)
		controller.RemoveNamespaceInformers(toRemove)
	}

	return nil
}

// =====================================================================
// ChaosSystemHandler - drives chaos-experiment registry from etcd events
// covering every injection.system.* sub-category.
// =====================================================================

// chaosSystemCategories enumerates the dynamic_config categories we bind to.
// Creating / deleting / toggling a system in etcd fires events under one of
// these categories; the handler fans them in and reconciles the registry.
func chaosSystemCategories() []string {
	return []string{
		"injection.system.count",
		"injection.system.ns_pattern",
		"injection.system.extract_pattern",
		"injection.system.display_name",
		"injection.system.app_label_key",
		"injection.system.is_builtin",
		"injection.system.status",
	}
}

// registrySyncer is the subset of chaos-experiment we call into from the
// watch handler. Kept as a package variable so tests can swap in a fake.
var registrySyncer chaosRegistrySyncer = defaultChaosRegistrySyncer{}

type chaosRegistrySyncer interface {
	IsRegistered(name string) bool
	Register(cfg chaos.SystemConfig) error
	Unregister(name string) error
}

type defaultChaosRegistrySyncer struct{}

func (defaultChaosRegistrySyncer) IsRegistered(name string) bool {
	return chaos.IsSystemRegistered(name)
}

func (defaultChaosRegistrySyncer) Register(cfg chaos.SystemConfig) error {
	return chaos.RegisterSystem(cfg)
}

func (defaultChaosRegistrySyncer) Unregister(name string) error {
	return chaos.UnregisterSystem(name)
}

type chaosSystemHandler struct {
	monitor    NamespaceMonitor
	controller *k8s.Controller
	publisher  common.ConfigPublisher
}

func newChaosSystemHandler(m NamespaceMonitor, c *k8s.Controller, publisher common.ConfigPublisher) *chaosSystemHandler {
	return &chaosSystemHandler{monitor: m, controller: c, publisher: publisher}
}

// forCategory returns a thin adapter that exposes a single category to the
// common ConfigHandler interface while delegating the actual work back to
// the shared reconcile method.
func (h *chaosSystemHandler) forCategory(category string) common.ConfigHandler {
	return &chaosSystemCategoryHandler{parent: h, category: category}
}

type chaosSystemCategoryHandler struct {
	parent   *chaosSystemHandler
	category string
}

func (h *chaosSystemCategoryHandler) Category() string { return h.category }

// Scope reports Global: injection.system.* is cross-process data (producer
// validates via SystemType.IsValid / GetAllSystemTypes, consumer applies the
// chaos runtime registry), so both sides subscribe to the same etcd prefix.
func (h *chaosSystemCategoryHandler) Scope() consts.ConfigScope {
	return consts.ConfigScopeGlobal
}

func (h *chaosSystemCategoryHandler) Handle(ctx context.Context, key, oldValue, newValue string) error {
	return h.parent.reconcile(ctx, key, oldValue, newValue)
}

// reconcile is the single entry point for every injection.system.* change.
// The config manager now reads Viper on demand so no explicit reload is
// needed — we sync the chaos-experiment registry for the affected system
// and trigger a namespace refresh when count/ns_pattern changes require it.
func (h *chaosSystemHandler) reconcile(ctx context.Context, key, oldValue, newValue string) error {
	return common.PublishWrapper(ctx, h.publisher, func() error {
		system, field := parseInjectionSystemKey(key)
		if system == "" {
			logrus.Warnf("ignoring non-system config change: %s", key)
			return nil
		}

		if err := h.syncRegistry(system); err != nil {
			return fmt.Errorf("failed to sync chaos-experiment registry for %s: %w", system, err)
		}

		// Only namespace-shaping fields require a monitor refresh.
		if field == "count" || field == "ns_pattern" || field == "status" {
			return h.refreshNamespaces()
		}
		return nil
	})
}

// syncRegistry reconciles the chaos-experiment registry for a single system
// against the current Viper/etcd state. New systems are registered, disabled
// systems are unregistered, and changes to NsPattern / AppLabelKey /
// DisplayName trigger a re-registration.
func (h *chaosSystemHandler) syncRegistry(name string) error {
	cfg, exists := config.GetChaosSystemConfigManager().Get(chaos.SystemType(name))
	if !exists {
		if registrySyncer.IsRegistered(name) {
			if err := registrySyncer.Unregister(name); err != nil {
				return fmt.Errorf("unregister %s after etcd delete: %w", name, err)
			}
			logrus.Infof("Unregistered system %s (no etcd state)", name)
		}
		return nil
	}

	if !cfg.IsEnabled() {
		if registrySyncer.IsRegistered(name) {
			if err := registrySyncer.Unregister(name); err != nil {
				return fmt.Errorf("unregister %s on disable: %w", name, err)
			}
			logrus.Infof("Unregistered system %s (status=disabled)", name)
		}
		return nil
	}

	// Always re-register so NsPattern / AppLabelKey / DisplayName edits take
	// effect. RegisterSystem is cheap and idempotent after an unregister.
	if registrySyncer.IsRegistered(name) {
		if err := registrySyncer.Unregister(name); err != nil {
			return fmt.Errorf("unregister %s before re-register: %w", name, err)
		}
	}
	appLabelKey := cfg.AppLabelKey
	if appLabelKey == "" {
		appLabelKey = "app"
	}
	if err := registrySyncer.Register(chaos.SystemConfig{
		Name:        name,
		NsPattern:   cfg.NsPattern,
		DisplayName: cfg.DisplayName,
		AppLabelKey: appLabelKey,
	}); err != nil {
		return fmt.Errorf("register %s: %w", name, err)
	}
	logrus.Infof("Registered system %s (ns_pattern=%q, app_label=%q)",
		name, cfg.NsPattern, appLabelKey)
	return nil
}

// parseInjectionSystemKey splits `injection.system.<name>.<field>` into
// its system / field parts. Returns empty strings for non-system keys so the
// caller can ignore them safely.
func parseInjectionSystemKey(key string) (system, field string) {
	const prefix = "injection.system."
	if !strings.HasPrefix(key, prefix) {
		return "", ""
	}
	rest := key[len(prefix):]
	// The system name is everything up to the last dot; the remainder is the
	// field name. System names today are single tokens (ts, otel-demo, …) so
	// a simple last-dot split is sufficient.
	idx := strings.LastIndex(rest, ".")
	if idx < 0 {
		return "", ""
	}
	return rest[:idx], rest[idx+1:]
}

func (h *chaosSystemHandler) refreshNamespaces() error {
	logrus.Info("Chaos system configuration updated, refreshing namespaces...")

	if h.monitor == nil {
		logrus.Warn("Monitor not initialized, skipping namespace refresh")
		return nil
	}

	result, err := h.monitor.RefreshNamespaces()
	if err != nil {
		return fmt.Errorf("failed to refresh namespaces: %w", err)
	}

	totalChanges := len(result.Added) + len(result.Recovered) + len(result.Disabled) + len(result.Deleted)
	logrus.Infof("Namespace refresh completed: %d total changes", totalChanges)

	if len(result.Added) > 0 {
		logrus.Infof("Added namespaces: %v", result.Added)
	}
	if len(result.Recovered) > 0 {
		logrus.Infof("Recovered namespaces: %v", result.Recovered)
	}
	if len(result.Disabled) > 0 {
		logrus.Warnf("Disabled namespaces (have active locks): %v", result.Disabled)
	}
	if len(result.Deleted) > 0 {
		logrus.Infof("Deleted namespaces (no active locks): %v", result.Deleted)
	}

	namespacesToAdd := make([]string, 0, len(result.Added)+len(result.Recovered))
	namespacesToAdd = append(namespacesToAdd, result.Added...)
	namespacesToAdd = append(namespacesToAdd, result.Recovered...)

	namespacesToRemove := make([]string, 0, len(result.Disabled)+len(result.Deleted))
	namespacesToRemove = append(namespacesToRemove, result.Disabled...)
	namespacesToRemove = append(namespacesToRemove, result.Deleted...)

	return UpdateK8sController(h.controller, namespacesToAdd, namespacesToRemove)
}

// =====================================================================
// RateLimitingConfigHandler - handles rate_limiting configuration
// =====================================================================

type rateLimitingConfigHandler struct {
	publisher      common.ConfigPublisher
	restartLimiter *TokenBucketRateLimiter
	buildLimiter   *TokenBucketRateLimiter
	algoLimiter    *TokenBucketRateLimiter
}

func newRateLimitingConfigHandler(
	publisher common.ConfigPublisher,
	restartLimiter *TokenBucketRateLimiter,
	buildLimiter *TokenBucketRateLimiter,
	algoLimiter *TokenBucketRateLimiter,
) *rateLimitingConfigHandler {
	return &rateLimitingConfigHandler{
		publisher:      publisher,
		restartLimiter: restartLimiter,
		buildLimiter:   buildLimiter,
		algoLimiter:    algoLimiter,
	}
}

func (h *rateLimitingConfigHandler) Category() string          { return "rate_limiting" }
func (h *rateLimitingConfigHandler) Scope() consts.ConfigScope { return consts.ConfigScopeConsumer }

func (h *rateLimitingConfigHandler) Handle(ctx context.Context, key, oldValue, newValue string) error {
	return common.PublishWrapper(ctx, h.publisher, func() error {
		logrus.WithFields(logrus.Fields{
			"key":       key,
			"old_value": oldValue,
			"new_value": newValue,
		}).Info("Rate limiting configuration updated, applying changes...")

		switch key {
		case "rate_limiting.max_concurrent_builds":
			if h.buildLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyBuildContainer)
				_, currentTimeout := h.buildLimiter.GetConfig()
				h.buildLimiter.UpdateConfig(maxTokens, currentTimeout)
			}

		case "rate_limiting.max_concurrent_restarts":
			if h.restartLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyRestartPedestal)
				_, currentTimeout := h.restartLimiter.GetConfig()
				h.restartLimiter.UpdateConfig(maxTokens, currentTimeout)
			}

		case "rate_limiting.max_concurrent_algo_execution":
			if h.algoLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyAlgoExecution)
				_, currentTimeout := h.algoLimiter.GetConfig()
				h.algoLimiter.UpdateConfig(maxTokens, currentTimeout)
			}

		case "rate_limiting.token_wait_timeout":
			tokenWaitTimeout := config.GetInt("rate_limiting.token_wait_timeout")
			timeout := time.Duration(tokenWaitTimeout) * time.Second

			if h.restartLimiter != nil {
				maxTokens, _ := h.restartLimiter.GetConfig()
				h.restartLimiter.UpdateConfig(maxTokens, timeout)
			}
			if h.buildLimiter != nil {
				maxTokens, _ := h.buildLimiter.GetConfig()
				h.buildLimiter.UpdateConfig(maxTokens, timeout)
			}
			if h.algoLimiter != nil {
				maxTokens, _ := h.algoLimiter.GetConfig()
				h.algoLimiter.UpdateConfig(maxTokens, timeout)
			}

		default:
			logrus.Warnf("Unknown rate limiting config key: %s, skipping update", key)
		}

		logrus.Info("Rate limiting configuration applied successfully")
		return nil
	})
}
