package initialization

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
	etcd "aegis/platform/etcd"
	"aegis/platform/model"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// MigrateLegacyInjectionSystem is the one-shot, idempotent upgrade path for
// pre-issue-75 installs. It reconciles three places where legacy data might
// still live so that Global etcd is the single source of truth:
//
//  1. Rows in the retired `systems` MySQL table are copied into
//     dynamic_configs (+ etcd Global prefix), then the table is dropped.
//  2. Any dynamic_configs rows under `injection.system.%` that still carry
//     the old Consumer scope are flipped to Global. The first iteration of
//     the follow-up PR seeded with Consumer scope before the scope change
//     landed, so existing installs on the intermediate commit need this.
//  3. Any etcd keys under the Consumer prefix matching
//     `injection.system.%` are copied to the Global prefix (taking existing
//     Global values as winners, so a live update from a Global writer beats
//     a stale Consumer copy) and the Consumer copies are deleted.
//
// Every branch is a no-op on a fresh install and safe to re-run.
func MigrateLegacyInjectionSystem(ctx context.Context, db *gorm.DB, etcdGw *etcd.Gateway) error {
	if db == nil {
		return nil
	}

	if err := drainLegacySystemsTable(ctx, db, etcdGw); err != nil {
		logrus.WithError(err).Warn("Failed to drain legacy systems table")
	}

	if err := rescopeDynamicConfigsToGlobal(db); err != nil {
		logrus.WithError(err).Warn("Failed to rescope injection.system.* dynamic_configs to Global")
	}

	// Guard against a typed-nil gateway leaking through the interface
	// boundary (a `(*etcd.Gateway)(nil)` wrapped into the interface is not
	// equal to a bare nil interface value).
	if etcdGw != nil {
		if err := drainConsumerEtcdIntoGlobal(ctx, etcdGw); err != nil {
			logrus.WithError(err).Warn("Failed to drain Consumer etcd prefix into Global")
		}
	}

	return nil
}

// drainLegacySystemsTable pulls rows from the retired `systems` table into
// dynamic_configs + etcd Global, then drops the table. The operation is
// idempotent — running it against a fresh install (no table) or an already
// migrated install (table missing or empty) is a no-op.
func drainLegacySystemsTable(ctx context.Context, db *gorm.DB, etcdGw *etcd.Gateway) error {
	if !db.Migrator().HasTable("systems") {
		return nil
	}

	type legacyRow struct {
		Name           string
		DisplayName    string
		NsPattern      string
		ExtractPattern string
		AppLabelKey    string
		Count          int
		Description    string
		IsBuiltin      bool
		Status         consts.StatusType
	}

	var rows []legacyRow
	if err := db.Table("systems").
		Select("name, display_name, ns_pattern, extract_pattern, app_label_key, count, description, is_builtin, status").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("read legacy systems table: %w", err)
	}

	migrated := 0
	for _, row := range rows {
		if row.Name == "" {
			continue
		}
		if err := migrateOneSystem(ctx, db, etcdGw, row); err != nil {
			logrus.WithError(err).Warnf("Failed to migrate legacy system %s", row.Name)
			continue
		}
		migrated++
	}

	if err := db.Migrator().DropTable("systems"); err != nil {
		return fmt.Errorf("drop legacy systems table: %w", err)
	}
	if migrated > 0 || len(rows) > 0 {
		logrus.Infof("Legacy systems table migrated (%d rows) and dropped", migrated)
	}
	return nil
}

// rescopeDynamicConfigsToGlobal rewrites the scope column for every
// `injection.system.*` row that is not already Global. GORM's UPDATE-with-WHERE
// is naturally idempotent: the second run touches zero rows.
func rescopeDynamicConfigsToGlobal(db *gorm.DB) error {
	result := db.Model(&model.DynamicConfig{}).
		Where("config_key LIKE ? AND scope <> ?", "injection.system.%", consts.ConfigScopeGlobal).
		Update("scope", consts.ConfigScopeGlobal)
	if result.Error != nil {
		return fmt.Errorf("rescope dynamic_configs: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		logrus.Infof("Rescoped %d injection.system.* dynamic_config row(s) to Global", result.RowsAffected)
	}
	return nil
}

// etcdMigrationClient is the minimal slice of the etcd gateway surface the
// drainer needs. Extracted as an interface so tests can stub it without
// spinning up a real etcd.
type etcdMigrationClient interface {
	ListPrefix(ctx context.Context, prefix string) ([]etcd.KeyValue, error)
	Get(ctx context.Context, key string) (string, error)
	Put(ctx context.Context, key, value string, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// drainConsumerEtcdIntoGlobal moves every `injection.system.*` key currently
// sitting under the Consumer etcd prefix to the Global prefix, then deletes
// the Consumer copy. Existing Global keys win (no overwrite) so a concurrent
// writer's fresh value is never clobbered by a stale Consumer copy.
func drainConsumerEtcdIntoGlobal(ctx context.Context, etcdGw etcdMigrationClient) error {
	if etcdGw == nil {
		return nil
	}

	consumerPrefix := consts.ConfigEtcdConsumerPrefix + "injection.system."

	listCtx, cancelList := context.WithTimeout(ctx, 5*time.Second)
	pairs, err := etcdGw.ListPrefix(listCtx, consumerPrefix)
	cancelList()
	if err != nil {
		return fmt.Errorf("list consumer etcd prefix: %w", err)
	}
	if len(pairs) == 0 {
		return nil
	}

	moved := 0
	for _, pair := range pairs {
		// Strip Consumer prefix to rebuild the relative key, then rebase onto
		// Global. This preserves the full `injection.system.<name>.<field>`
		// suffix even if it contains additional dots in the future.
		if !strings.HasPrefix(pair.Key, consts.ConfigEtcdConsumerPrefix) {
			continue
		}
		configKey := strings.TrimPrefix(pair.Key, consts.ConfigEtcdConsumerPrefix)
		globalKey := consts.ConfigEtcdGlobalPrefix + configKey

		getCtx, cancelGet := context.WithTimeout(ctx, 5*time.Second)
		existing, getErr := etcdGw.Get(getCtx, globalKey)
		cancelGet()
		// Gateway returns an error when the key is not found; treat any error
		// as "not set" so we can seed it.
		if getErr != nil || existing == "" {
			putCtx, cancelPut := context.WithTimeout(ctx, 5*time.Second)
			if err := etcdGw.Put(putCtx, globalKey, pair.Value, 0); err != nil {
				cancelPut()
				logrus.WithError(err).Warnf("Failed to copy %s to Global prefix", configKey)
				continue
			}
			cancelPut()
			moved++
		}

		delCtx, cancelDel := context.WithTimeout(ctx, 5*time.Second)
		if err := etcdGw.Delete(delCtx, pair.Key); err != nil {
			cancelDel()
			logrus.WithError(err).Warnf("Failed to delete Consumer etcd copy of %s", configKey)
			continue
		}
		cancelDel()
	}

	if moved > 0 {
		logrus.Infof("Drained %d injection.system.* etcd key(s) from Consumer prefix to Global", moved)
	} else {
		logrus.Infof("Cleaned %d stale injection.system.* key(s) from Consumer etcd prefix", len(pairs))
	}
	return nil
}

// migrateOneSystem writes (or refreshes) the 7 dynamic_config rows for a
// legacy system row and publishes the values to etcd under the Global
// prefix. If a row already exists we leave its ID / timestamps alone so
// existing config_histories stay valid.
func migrateOneSystem(ctx context.Context, db *gorm.DB, etcdGw *etcd.Gateway, row struct {
	Name           string
	DisplayName    string
	NsPattern      string
	ExtractPattern string
	AppLabelKey    string
	Count          int
	Description    string
	IsBuiltin      bool
	Status         consts.StatusType
}) error {
	appLabel := row.AppLabelKey
	if appLabel == "" {
		appLabel = "app"
	}
	// `systems.status` is NOT NULL (consts.StatusType with 0=disabled,
	// 1=enabled, -1=deleted). Use the value as-is — we must never silently
	// flip a disabled legacy row to enabled during migration.

	seeds := []struct {
		field     string
		value     string
		valueType consts.ConfigValueType
	}{
		{"count", strconv.Itoa(row.Count), consts.ConfigValueTypeInt},
		{"ns_pattern", row.NsPattern, consts.ConfigValueTypeString},
		{"extract_pattern", row.ExtractPattern, consts.ConfigValueTypeString},
		{"display_name", row.DisplayName, consts.ConfigValueTypeString},
		{"app_label_key", appLabel, consts.ConfigValueTypeString},
		{"is_builtin", strconv.FormatBool(row.IsBuiltin), consts.ConfigValueTypeBool},
		{"status", strconv.Itoa(int(row.Status)), consts.ConfigValueTypeInt},
	}

	for _, seed := range seeds {
		key := fmt.Sprintf("injection.system.%s.%s", row.Name, seed.field)

		var existing model.DynamicConfig
		err := db.Where("config_key = ?", key).First(&existing).Error
		switch err {
		case nil:
			// Row already exists — refresh the default value and move on.
			if existing.DefaultValue != seed.value {
				if err := db.Model(&existing).
					Update("default_value", seed.value).Error; err != nil {
					return fmt.Errorf("refresh default for %s: %w", key, err)
				}
			}
		case gorm.ErrRecordNotFound:
			cfg := &model.DynamicConfig{
				Key:          key,
				DefaultValue: seed.value,
				ValueType:    seed.valueType,
				Scope:        consts.ConfigScopeGlobal,
				Category:     "injection.system." + seed.field,
				Description:  legacyFieldDescription(row.DisplayName, row.Description, seed.field),
			}
			if err := db.Create(cfg).Error; err != nil {
				return fmt.Errorf("create legacy config %s: %w", key, err)
			}
		default:
			return fmt.Errorf("lookup %s: %w", key, err)
		}

		if etcdGw != nil {
			etcdKey := consts.ConfigEtcdGlobalPrefix + key
			putCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := etcdGw.Put(putCtx, etcdKey, seed.value, 0); err != nil {
				cancel()
				return fmt.Errorf("publish %s to etcd: %w", key, err)
			}
			cancel()
		}

		// Keep Viper in sync so any code that reads in the same process
		// sees the fresh value immediately.
		_ = config.SetViperValue(key, seed.value, seed.valueType)
	}

	return nil
}

// legacyFieldDescription preserves the original `systems.description` only
// on the anchor `count` row; other fields get generated descriptions so the
// dynamic_config list UI stays readable.
func legacyFieldDescription(displayName, tableDesc, field string) string {
	switch field {
	case "count":
		if tableDesc != "" {
			return tableDesc
		}
		return fmt.Sprintf("Number of system %s to create", displayName)
	case "ns_pattern":
		return fmt.Sprintf("Namespace pattern for system %s instances", displayName)
	case "extract_pattern":
		return fmt.Sprintf("Extraction pattern for namespace prefix and number from %s instances", displayName)
	case "display_name":
		return fmt.Sprintf("Human-readable display name for system %s", displayName)
	case "app_label_key":
		return fmt.Sprintf("Kubernetes pod label key used to select %s workloads", displayName)
	case "is_builtin":
		return fmt.Sprintf("Whether %s is a builtin benchmark system", displayName)
	case "status":
		return fmt.Sprintf("Status of system %s (1=enabled, 0=disabled, -1=deleted)", displayName)
	}
	return ""
}
