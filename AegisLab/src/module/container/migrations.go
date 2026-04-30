package container

import (
	"fmt"

	"aegis/framework"
	"aegis/model"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "container",
		Entities: []interface{}{
			&model.Container{},
			&model.ContainerVersion{},
			&model.HelmConfig{},
			&model.ParameterConfig{},
			&model.ContainerLabel{},
			&model.ContainerVersionEnvVar{},
			&model.HelmConfigValue{},
			&model.UserContainer{},
		},
		PreMigrate: preMigrateParameterConfigsSystemScope,
	}
}

// preMigrateParameterConfigsSystemScope handles the issue #314 transition:
// the parameter_configs unique index was (config_key, type, category) and is
// becoming (system_id, config_key, type, category). AutoMigrate will not drop
// the old index automatically — it only creates new indexes — so we drop it
// here before the schema sweep adds the new one. We also backfill system_id
// for rows we can attribute to a single container via existing
// helm_config_values / container_version_env_vars links, so the existing
// per-system uniqueness contract holds on first boot after the upgrade.
//
// Idempotent: subsequent boots find no idx_unique_config row matching the old
// shape and no rows needing backfill.
func preMigrateParameterConfigsSystemScope(db *gorm.DB) error {
	if !db.Migrator().HasTable("parameter_configs") {
		return nil
	}
	if !db.Migrator().HasColumn(&model.ParameterConfig{}, "system_id") {
		// New column will be added by AutoMigrate. Drop the old unique
		// index first so AutoMigrate's "create new uniqueIndex" succeeds
		// without colliding on the legacy 3-column constraint.
		if db.Migrator().HasIndex(&model.ParameterConfig{}, "idx_unique_config") {
			if err := db.Migrator().DropIndex(&model.ParameterConfig{}, "idx_unique_config"); err != nil {
				return fmt.Errorf("preMigrate parameter_configs: drop legacy idx_unique_config: %w", err)
			}
		}
		return nil
	}
	// system_id column already present. The legacy index may still exist on
	// a partially-migrated DB; drop it defensively. AutoMigrate will then
	// recreate idx_unique_config on the new 4-column shape.
	if db.Migrator().HasIndex(&model.ParameterConfig{}, "idx_unique_config") {
		// On MySQL we can detect the legacy 3-column index by checking
		// information_schema. Rather than dialect-branch we just drop and
		// let AutoMigrate recreate it; AutoMigrate is idempotent on
		// matching shapes.
		var cols []struct{ ColumnName string }
		_ = db.Raw(`SELECT COLUMN_NAME AS column_name
			FROM INFORMATION_SCHEMA.STATISTICS
			WHERE TABLE_SCHEMA = DATABASE()
			  AND TABLE_NAME = 'parameter_configs'
			  AND INDEX_NAME = 'idx_unique_config'`).Scan(&cols).Error
		if len(cols) > 0 && len(cols) < 4 {
			if err := db.Migrator().DropIndex(&model.ParameterConfig{}, "idx_unique_config"); err != nil {
				return fmt.Errorf("preMigrate parameter_configs: drop legacy idx_unique_config: %w", err)
			}
		}
	}
	return backfillParameterConfigSystemID(db)
}

// backfillParameterConfigSystemID resolves system_id for existing
// parameter_configs rows. A row gets its owning containers.id when every link
// (helm_config_values + container_version_env_vars) ties it to the same
// container; rows linked to multiple containers stay NULL (cluster-wide /
// shared) — those are the rows that motivated the original 3-column unique
// index and we keep that shape for them via system_id IS NULL.
//
// The byte-cluster backfill for global.otel.endpoint is NOT done here — that
// data comes from data.yaml during reseed, which runs after this hook. The
// operator triggers it via `aegisctl system reseed --apply` (see issue #314
// PR body for the runbook). This hook only repairs the schema and links the
// existing single-owner rows to their natural owners so reseed can find them.
func backfillParameterConfigSystemID(db *gorm.DB) error {
	// Skip on dialects without the joins we use; sqlite test DBs hand-create
	// parameter_configs without system_id column anyway, so this branch only
	// matters on MySQL.
	if db.Dialector.Name() != "mysql" {
		return nil
	}
	// Stamp system_id only when a parameter_config has exactly one owner across
	// the UNION of both link tables (helm_config_values + container_version_env_vars).
	// A row that's single-owner via one link but multi-owner via the other is
	// ambiguous (cluster-wide / shared) and stays NULL — preserving the
	// invariant documented above backfillParameterConfigSystemID.
	unionSQL := `
		UPDATE parameter_configs pc
		JOIN (
			SELECT pid, MIN(cid) AS cid
			FROM (
				SELECT hcv.parameter_config_id AS pid, cv.container_id AS cid
				FROM helm_config_values hcv
				JOIN helm_configs hc ON hc.id = hcv.helm_config_id
				JOIN container_versions cv ON cv.id = hc.container_version_id
				UNION
				SELECT cvev.parameter_config_id AS pid, cv.container_id AS cid
				FROM container_version_env_vars cvev
				JOIN container_versions cv ON cv.id = cvev.container_version_id
			) links
			GROUP BY pid
			HAVING MIN(cid) = MAX(cid)
		) owned ON owned.pid = pc.id
		SET pc.system_id = owned.cid
		WHERE pc.system_id IS NULL`
	if err := db.Exec(unionSQL).Error; err != nil {
		logrus.Warnf("preMigrate parameter_configs: backfill system_id: %v", err)
	}
	return nil
}
