package consumer

import (
	"context"
	"fmt"
	"time"

	"aegis/core/orchestrator/common"
	"aegis/platform/config"
	"aegis/platform/consts"
	k8s "aegis/platform/k8s"

	"github.com/sirupsen/logrus"
)

// RegisterConsumerHandlers registers the configuration handlers that the
// consumer process owns. Should be called during consumer initialization,
// after RegisterGlobalHandlers.
func RegisterConsumerHandlers(
	controller *k8s.Controller,
	monitor NamespaceMonitor,
	publisher common.ConfigPublisher,
	restartLimiter *TokenBucketRateLimiter,
	warmingLimiter *TokenBucketRateLimiter,
	buildLimiter *TokenBucketRateLimiter,
	buildDatapackLimiter *TokenBucketRateLimiter,
	algoLimiter *TokenBucketRateLimiter,
) {
	if monitor != nil && controller != nil {
		monitor.SetActivator(controller)
	}

	common.RegisterHandler(newRateLimitingConfigHandler(
		publisher,
		restartLimiter,
		warmingLimiter,
		buildLimiter,
		buildDatapackLimiter,
		algoLimiter,
	))
	consumerScope := consts.ConfigScopeConsumer
	globalScope := consts.ConfigScopeGlobal
	logrus.Infof("Registered consumer config handlers: consumer=%v global=%v",
		common.ListRegisteredConfigKeys(&consumerScope),
		common.ListRegisteredConfigKeys(&globalScope))
}

// ReconcileRateLimitersFromConfig re-applies the current config values to each
// limiter. Limiters are fx-constructed before the config listener loads
// etcd/DB overrides into viper, so a fresh boot would otherwise honour only
// the in-binary const default — an operator override set before the worker
// started would be ignored until a live UpdateConfig that never comes. Call
// this once after the config scopes are activated.
func ReconcileRateLimitersFromConfig(
	restartLimiter *TokenBucketRateLimiter,
	warmingLimiter *TokenBucketRateLimiter,
	buildLimiter *TokenBucketRateLimiter,
	buildDatapackLimiter *TokenBucketRateLimiter,
	algoLimiter *TokenBucketRateLimiter,
) {
	apply := func(limiter *TokenBucketRateLimiter, maxTokensKey string) {
		if limiter == nil {
			return
		}
		maxTokens := config.GetInt(maxTokensKey)
		_, currentTimeout := limiter.GetConfig()
		if waitSecs := config.GetInt(consts.TokenWaitTimeoutKey); waitSecs > 0 {
			currentTimeout = time.Duration(waitSecs) * time.Second
		}
		limiter.UpdateConfig(maxTokens, currentTimeout)
	}

	apply(restartLimiter, consts.MaxTokensKeyRestartPedestal)
	apply(warmingLimiter, consts.MaxTokensKeyNamespaceWarming)
	apply(buildLimiter, consts.MaxTokensKeyBuildContainer)
	apply(buildDatapackLimiter, consts.MaxTokensKeyBuildDatapack)
	apply(algoLimiter, consts.MaxTokensKeyAlgoExecution)
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
// RateLimitingConfigHandler - handles rate_limiting configuration
// =====================================================================

type rateLimitingConfigHandler struct {
	publisher            common.ConfigPublisher
	restartLimiter       *TokenBucketRateLimiter
	warmingLimiter       *TokenBucketRateLimiter
	buildLimiter         *TokenBucketRateLimiter
	buildDatapackLimiter *TokenBucketRateLimiter
	algoLimiter          *TokenBucketRateLimiter
}

func newRateLimitingConfigHandler(
	publisher common.ConfigPublisher,
	restartLimiter *TokenBucketRateLimiter,
	warmingLimiter *TokenBucketRateLimiter,
	buildLimiter *TokenBucketRateLimiter,
	buildDatapackLimiter *TokenBucketRateLimiter,
	algoLimiter *TokenBucketRateLimiter,
) *rateLimitingConfigHandler {
	return &rateLimitingConfigHandler{
		publisher:            publisher,
		restartLimiter:       restartLimiter,
		warmingLimiter:       warmingLimiter,
		buildLimiter:         buildLimiter,
		buildDatapackLimiter: buildDatapackLimiter,
		algoLimiter:          algoLimiter,
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

		// The watched key, consts.MaxTokensKey*, and the config.GetInt read
		// must be the identical string for the reload to apply — a divergence
		// silently no-ops the operator-set value (the restart case shipped with
		// "rate_limiting.max_concurrent_restarts" while the read used the
		// _pedestal-suffixed const, so an etcd put never moved the limiter).
		switch key {
		case consts.MaxTokensKeyBuildContainer:
			if h.buildLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyBuildContainer)
				_, currentTimeout := h.buildLimiter.GetConfig()
				h.buildLimiter.UpdateConfig(maxTokens, currentTimeout)
			}

		case consts.MaxTokensKeyBuildDatapack:
			if h.buildDatapackLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyBuildDatapack)
				_, currentTimeout := h.buildDatapackLimiter.GetConfig()
				h.buildDatapackLimiter.UpdateConfig(maxTokens, currentTimeout)
			}

		case consts.MaxTokensKeyRestartPedestal:
			if h.restartLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyRestartPedestal)
				_, currentTimeout := h.restartLimiter.GetConfig()
				h.restartLimiter.UpdateConfig(maxTokens, currentTimeout)
			}

		case consts.MaxTokensKeyNamespaceWarming:
			if h.warmingLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyNamespaceWarming)
				_, currentTimeout := h.warmingLimiter.GetConfig()
				h.warmingLimiter.UpdateConfig(maxTokens, currentTimeout)
			}

		case consts.MaxTokensKeyAlgoExecution:
			if h.algoLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyAlgoExecution)
				_, currentTimeout := h.algoLimiter.GetConfig()
				h.algoLimiter.UpdateConfig(maxTokens, currentTimeout)
			}

		case consts.TokenWaitTimeoutKey:
			tokenWaitTimeout := config.GetInt(consts.TokenWaitTimeoutKey)
			timeout := time.Duration(tokenWaitTimeout) * time.Second

			if h.restartLimiter != nil {
				maxTokens, _ := h.restartLimiter.GetConfig()
				h.restartLimiter.UpdateConfig(maxTokens, timeout)
			}
			if h.warmingLimiter != nil {
				maxTokens, _ := h.warmingLimiter.GetConfig()
				h.warmingLimiter.UpdateConfig(maxTokens, timeout)
			}
			if h.buildLimiter != nil {
				maxTokens, _ := h.buildLimiter.GetConfig()
				h.buildLimiter.UpdateConfig(maxTokens, timeout)
			}
			if h.buildDatapackLimiter != nil {
				maxTokens, _ := h.buildDatapackLimiter.GetConfig()
				h.buildDatapackLimiter.UpdateConfig(maxTokens, timeout)
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
