package initialization

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"aegis/config"
	"aegis/consts"
	etcd "aegis/infra/etcd"
	k8s "aegis/infra/k8s"
	redis "aegis/infra/redis"
	"aegis/model"
	ratelimiter "aegis/module/ratelimiter"
	"aegis/service/common"
	"aegis/service/consumer"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func InitializeConsumer(
	ctx context.Context,
	db *gorm.DB,
	controller *k8s.Controller,
	monitor consumer.NamespaceMonitor,
	publisher *redis.Gateway,
	etcdGw *etcd.Gateway,
	listener *common.ConfigUpdateListener,
	restartLimiter *consumer.TokenBucketRateLimiter,
	buildLimiter *consumer.TokenBucketRateLimiter,
	algoLimiter *consumer.TokenBucketRateLimiter,
) error {
	consumerData, err := newConfigDataWithDB(db, consts.ConfigScopeConsumer)
	if err != nil {
		return fmt.Errorf("failed to load consumer config metadata: %w", err)
	}

	if len(consumerData.configs) == 0 {
		logrus.Info("Seeding initial system data for consumer...")
		if err := initializeConsumer(ctx, db, etcdGw); err != nil {
			return fmt.Errorf("failed to initialize system data for consumer: %w", err)
		}
		logrus.Info("Successfully seeded initial system data for consumer")
	} else {
		logrus.Info("Initial system data for consumer already seeded, skipping initialization")
	}

	common.RegisterGlobalHandlers(publisher)
	consumer.RegisterConsumerHandlers(controller, monitor, publisher, restartLimiter, buildLimiter, algoLimiter)
	if err := activateConfigScope(consumerData.scope, listener); err != nil {
		return err
	}

	// Auto-GC leaked rate-limiter tokens on startup (OperationsPAI/aegis#21).
	rlSvc := ratelimiter.NewService(publisher, db)
	if released, buckets, err := rlSvc.GC(ctx); err != nil {
		logrus.WithError(err).Warn("rate-limiter startup GC failed")
	} else if released > 0 {
		logrus.WithFields(logrus.Fields{"released": released, "buckets": buckets}).
			Info("rate-limiter startup GC completed")
	}

	// Namespace/bootstrap informer initialization can take noticeably longer than
	// the Fx startup deadline when the local cluster is cold or slow. Run it in
	// the background so consumer/both startup does not fail with
	// "context deadline exceeded" during local debugging.
	if monitor == nil {
		logrus.Warn("Monitor not initialized, skipping namespace initialization")
		return nil
	}

	monitor.SetContext(ctx)
	go func() {
		logrus.Info("Initializing namespaces on startup...")

		initialized, err := monitor.InitializeNamespaces()
		if err != nil {
			logrus.Errorf("Failed to initialize namespaces: %v", err)
			return
		}

		if len(initialized) == 0 {
			logrus.Warn("No namespaces to initialize on startup")
			return
		}

		logrus.Infof("Initialized namespaces on startup: %v", initialized)
		if err := consumer.UpdateK8sController(controller, initialized, []string{}); err != nil {
			logrus.Errorf("Failed to update k8s controller: %v", err)
		}
	}()

	return nil
}

func initializeConsumer(ctx context.Context, db *gorm.DB, etcdGw *etcd.Gateway) error {
	dataPath := config.GetString("initialization.data_path")
	filePath := filepath.Join(dataPath, consts.InitialFilename)
	initialData, err := loadInitialDataFromFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to load initial data from file: %w", err)
	}

	var seededConfigs []model.DynamicConfig
	err = withOptimizedDBSettings(db, func() error {
		txErr := db.Transaction(func(tx *gorm.DB) error {
			configs, err := initializeDynamicConfigs(tx, initialData)
			if err != nil {
				return fmt.Errorf("failed to initialize dynamic configs for consumer: %w", err)
			}
			seededConfigs = configs
			return nil
		})
		if txErr != nil {
			return fmt.Errorf("failed to initialize consumer data: %w", txErr)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Publish seeded defaults to etcd AFTER the DB transaction commits so we
	// never leak pending DB state into etcd. Mirrors the legacy-migration
	// publish path in legacy_systems_migration.go so a fresh cluster boots
	// with /rcabench/config/<scope>/... populated for every seed row (else
	// backend logs `loaded N systems (0 enabled)`).
	publishSeededConfigsToEtcd(ctx, etcdGw, seededConfigs)
	return nil
}

func initializeDynamicConfigs(tx *gorm.DB, data *InitialData) ([]model.DynamicConfig, error) {
	var configs []model.DynamicConfig
	for _, configData := range data.DynamicConfigs {
		cfg := configData.ConvertToDBDynamicConfig()
		if err := common.ValidateConfigMetadataConstraints(cfg); err != nil {
			return nil, fmt.Errorf("invalid config value for key %s: %w", configData.Key, err)
		}

		if err := common.CreateConfig(tx, cfg); err != nil {
			return nil, fmt.Errorf("failed to create dynamic config %s: %w", configData.Key, err)
		}

		configs = append(configs, *cfg)
	}

	return configs, nil
}

// seedEtcdPrefixForScope mirrors service/common.scopePrefix (and
// module/system.etcdPrefixForScope) without pulling those packages in as a
// dependency for seeding. Scopes without an etcd representation return "".
func seedEtcdPrefixForScope(scope consts.ConfigScope) string {
	switch scope {
	case consts.ConfigScopeProducer:
		return consts.ConfigEtcdProducerPrefix
	case consts.ConfigScopeConsumer:
		return consts.ConfigEtcdConsumerPrefix
	case consts.ConfigScopeGlobal:
		return consts.ConfigEtcdGlobalPrefix
	default:
		return ""
	}
}

// publishSeededConfigsToEtcd writes each seeded row's DefaultValue to etcd
// under `<scope-prefix><config_key>`, but only when the key is absent. This
// keeps the operation idempotent across restarts and avoids stomping any
// live override that may have been written between DB seed and this call.
// Gateway errors are logged, not propagated — a missing etcd endpoint (e.g.
// in unit tests) must not break the DB seed path.
func publishSeededConfigsToEtcd(ctx context.Context, etcdGw *etcd.Gateway, configs []model.DynamicConfig) {
	if etcdGw == nil {
		logrus.Info("seed: etcd gateway unavailable, skipping default_value publish for seeded dynamic_configs")
		return
	}
	for i := range configs {
		cfg := &configs[i]
		prefix := seedEtcdPrefixForScope(cfg.Scope)
		if prefix == "" {
			continue
		}
		etcdKey := prefix + cfg.Key

		getCtx, getCancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := etcdGw.Get(getCtx, etcdKey)
		getCancel()
		if err == nil {
			// Key already present — never overwrite; a live operator value
			// (or a previous seed) takes precedence.
			continue
		}
		// Gateway.Get returns an ad-hoc "key not found: <key>" error when the
		// key is absent. Any other error (connection, auth) — log and skip.
		if !strings.Contains(err.Error(), "key not found") {
			logrus.WithError(err).Warnf("seed: etcd lookup failed for %s, skipping publish", etcdKey)
			continue
		}

		putCtx, putCancel := context.WithTimeout(ctx, 5*time.Second)
		putErr := etcdGw.Put(putCtx, etcdKey, cfg.DefaultValue, 0)
		putCancel()
		if putErr != nil {
			logrus.WithError(putErr).Warnf("seed: failed to publish %s to etcd", etcdKey)
			continue
		}
		logrus.Infof("seed: published %s to etcd", etcdKey)
	}
}
