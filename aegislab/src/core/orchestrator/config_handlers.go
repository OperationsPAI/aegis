package consumer

import (
	"context"
	"fmt"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
	k8s "aegis/platform/k8s"
	"aegis/core/orchestrator/common"

	"github.com/sirupsen/logrus"
)

// RegisterConsumerHandlers registers the configuration handlers that the
// consumer process owns. §11 step 5c removed the etcd→chaos-experiment
// registry sync: chaos-service has its own registry now and a live
// `aegisctl chaos system update` no longer reflects into the in-process
// registry — boot-time InitializeSystems is the only path until phase 2
// finishes the chaos-experiment migration.
//
// Should be called during consumer initialization, after RegisterGlobalHandlers.
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

		switch key {
		case "rate_limiting.max_concurrent_builds":
			// consts.MaxTokensKeyBuildContainer now points at the same
			// "rate_limiting.max_concurrent_builds" key the watcher fires
			// on; previously they disagreed and the operator-set value was
			// silently ignored on reload.
			if h.buildLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyBuildContainer)
				_, currentTimeout := h.buildLimiter.GetConfig()
				h.buildLimiter.UpdateConfig(maxTokens, currentTimeout)
			}

		case "rate_limiting.max_concurrent_build_datapack":
			if h.buildDatapackLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyBuildDatapack)
				_, currentTimeout := h.buildDatapackLimiter.GetConfig()
				h.buildDatapackLimiter.UpdateConfig(maxTokens, currentTimeout)
			}

		case "rate_limiting.max_concurrent_restarts":
			if h.restartLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyRestartPedestal)
				_, currentTimeout := h.restartLimiter.GetConfig()
				h.restartLimiter.UpdateConfig(maxTokens, currentTimeout)
			}

		case "rate_limiting.max_concurrent_ns_warming":
			if h.warmingLimiter != nil {
				maxTokens := config.GetInt(consts.MaxTokensKeyNamespaceWarming)
				_, currentTimeout := h.warmingLimiter.GetConfig()
				h.warmingLimiter.UpdateConfig(maxTokens, currentTimeout)
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
