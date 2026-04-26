package initialization

import (
	"context"
	"encoding/json"
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
	"gorm.io/gorm/clause"
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
	warmingLimiter *consumer.TokenBucketRateLimiter,
	buildLimiter *consumer.TokenBucketRateLimiter,
	buildDatapackLimiter *consumer.TokenBucketRateLimiter,
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
		// Prerequisites (issue #115) are additive metadata, not the big
		// first-boot bootstrap. Reconcile them on every boot so a data.yaml
		// prereq addition (e.g. new sockshop chart) lands without requiring
		// a full reseed. ReconcileSystemPrerequisites is idempotent and does
		// not stomp a reconciled status for unchanged specs.
		if err := reconcilePrerequisitesFromDataFile(db); err != nil {
			logrus.WithError(err).Warn("Failed to reconcile system prerequisites from data.yaml")
		}
	}

	common.RegisterGlobalHandlers(publisher)
	consumer.RegisterConsumerHandlers(controller, monitor, publisher, restartLimiter, warmingLimiter, buildLimiter, buildDatapackLimiter, algoLimiter)
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
			if err := initializeSystemPrerequisites(tx, initialData); err != nil {
				return fmt.Errorf("failed to initialize system prerequisites: %w", err)
			}
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

// initializeSystemPrerequisites seeds the `system_prerequisites` table from
// the `prerequisites` list attached to each type=2 (pedestal) container entry
// in data.yaml. Idempotent: re-running the seed never duplicates a row; on a
// re-seed the spec_json is refreshed but the status column is preserved so a
// previously reconciled prereq stays reconciled until aegisctl marks it
// otherwise (issue #115).
func initializeSystemPrerequisites(tx *gorm.DB, data *InitialData) error {
	for _, c := range data.Containers {
		if c.Type != consts.ContainerTypePedestal {
			continue
		}
		for _, p := range c.Prerequisites {
			row, err := buildPrerequisiteRow(c.Name, p)
			if err != nil {
				return err
			}
			if err := upsertSystemPrerequisite(tx, row); err != nil {
				return err
			}
		}
	}
	return nil
}

// reconcilePrerequisitesFromDataFile is the every-boot path: it re-reads
// data.yaml and upserts prereqs without touching the rest of the seed. Gate
// failures here are non-fatal (logged, not propagated) so a bad prereq row
// never keeps the consumer from starting.
func reconcilePrerequisitesFromDataFile(db *gorm.DB) error {
	dataPath := config.GetString("initialization.data_path")
	filePath := filepath.Join(dataPath, consts.InitialFilename)
	data, err := loadInitialDataFromFile(filePath)
	if err != nil {
		return fmt.Errorf("load data.yaml: %w", err)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return initializeSystemPrerequisites(tx, data)
	})
}

// buildPrerequisiteRow converts one InitialSystemPrerequisite into a DB row.
// Unknown kinds are accepted (and stored as-is) so future kinds can be added
// to data.yaml without a code change; only the helm-kind payload is validated
// here because it's the only one aegisctl currently knows how to reconcile.
func buildPrerequisiteRow(systemName string, p InitialSystemPrerequisite) (*model.SystemPrerequisite, error) {
	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("prerequisite for system %q has empty name", systemName)
	}
	kind := strings.TrimSpace(p.Kind)
	if kind == "" {
		kind = model.SystemPrerequisiteKindHelm
	}
	spec := map[string]any{}
	switch kind {
	case model.SystemPrerequisiteKindHelm:
		if strings.TrimSpace(p.Chart) == "" {
			return nil, fmt.Errorf("prerequisite %q for system %q: chart is required for kind=helm", p.Name, systemName)
		}
		spec["chart"] = p.Chart
		spec["namespace"] = p.Namespace
		spec["version"] = p.Version
		if len(p.Values) > 0 {
			values := make([]map[string]string, 0, len(p.Values))
			for _, v := range p.Values {
				if strings.TrimSpace(v.Key) == "" {
					continue
				}
				values = append(values, map[string]string{
					"key":   v.Key,
					"value": v.Value,
				})
			}
			if len(values) > 0 {
				spec["values"] = values
			}
		}
	default:
		// Best-effort: preserve every non-Name/Kind field we know about so a
		// new-kind reader can pick them up without a new seeder.
		if p.Chart != "" {
			spec["chart"] = p.Chart
		}
		if p.Namespace != "" {
			spec["namespace"] = p.Namespace
		}
		if p.Version != "" {
			spec["version"] = p.Version
		}
		if len(p.Values) > 0 {
			values := make([]map[string]string, 0, len(p.Values))
			for _, v := range p.Values {
				if strings.TrimSpace(v.Key) == "" {
					continue
				}
				values = append(values, map[string]string{
					"key":   v.Key,
					"value": v.Value,
				})
			}
			if len(values) > 0 {
				spec["values"] = values
			}
		}
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec for prerequisite %q of system %q: %w", p.Name, systemName, err)
	}
	return &model.SystemPrerequisite{
		SystemName: systemName,
		Kind:       kind,
		Name:       p.Name,
		SpecJSON:   string(raw),
		Status:     model.SystemPrerequisiteStatusPending,
	}, nil
}

// upsertSystemPrerequisite keeps spec_json up-to-date for existing rows while
// leaving the `status` column untouched on conflict — we never want a
// re-seed (which runs every boot) to revert a `reconciled` prereq back to
// `pending`. aegisctl is the only writer of the status column.
func upsertSystemPrerequisite(tx *gorm.DB, row *model.SystemPrerequisite) error {
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "system_name"}, {Name: "kind"}, {Name: "name"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"spec_json":  row.SpecJSON,
			"updated_at": time.Now(),
		}),
	}).Create(row).Error
}
