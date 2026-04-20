package initialization

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"aegis/config"
	"aegis/consts"
	etcd "aegis/infra/etcd"
	"aegis/model"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// MigrateLegacySystemsTable pulls any rows from the retired `systems` table
// into the dynamic_configs table + etcd, then drops the table. The operation
// is idempotent — running it against a fresh install (no table) or an already
// migrated install (table missing or empty) is a no-op.
//
// Pre-issue-75 behaviour: the systems table was the single source of truth
// for per-system runtime knobs (count / ns_pattern / status …).
// Post-issue-75 behaviour: etcd (seeded by dynamic_configs) is the source of
// truth. This migration bridges the gap on upgrade.
func MigrateLegacySystemsTable(ctx context.Context, db *gorm.DB, etcdGw *etcd.Gateway) error {
	if db == nil {
		return nil
	}
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
		logrus.WithError(err).Warn("Failed to read legacy systems table; skipping migration")
		return nil
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
		logrus.WithError(err).Warn("Failed to drop legacy systems table; will retry next boot")
	} else if migrated > 0 || len(rows) > 0 {
		logrus.Infof("Legacy systems table migrated (%d rows) and dropped", migrated)
	}

	return nil
}

// migrateOneSystem writes (or refreshes) the 7 dynamic_config rows for a
// legacy system row and publishes the values to etcd. If a row already
// exists we leave its ID / timestamps alone so existing config_histories
// stay valid.
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
	status := row.Status
	if status == 0 {
		status = consts.CommonEnabled
	}

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
		{"status", strconv.Itoa(int(status)), consts.ConfigValueTypeInt},
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
				Scope:        consts.ConfigScopeConsumer,
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
			etcdKey := consts.ConfigEtcdConsumerPrefix + key
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
