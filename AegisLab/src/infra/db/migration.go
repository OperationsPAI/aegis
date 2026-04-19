package db

import (
	"aegis/framework"
	"aegis/model"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// centralEntities is the Phase 3 coexistence list. Phase 4 PRs remove
// entities from this slice as their owning module contributes a
// `framework.MigrationRegistrar`. AutoMigrate is idempotent so overlap
// during a transition PR is harmless.
func centralEntities() []interface{} {
	return []interface{}{
		&model.Dataset{},
		&model.DatasetVersion{},
		// &model.Label{} migrated to module/label/migrations.go (Phase 3
		// reference migration). Future Phase 4 PRs drop their entities
		// the same way — remove from this slice, add a
		// framework.MigrationRegistrar in the owning module.
		// &model.User{} migrated to module/user/migrations.go (Phase 4).
		// &model.APIKey{} stays here until module/auth Phase-4 PR (issue #39)
		// claims it — it's owned by auth, not user.
		&model.APIKey{},
		&model.AuditLog{},
		&model.Task{},
		&model.FaultInjection{},
		&model.Execution{},
		&model.DetectorResult{},
		&model.GranularityResult{},
		&model.DatasetLabel{},
		&model.DatasetVersionInjection{},
		&model.FaultInjectionLabel{},
		&model.ExecutionInjectionLabel{},
		&model.ConfigLabel{},
		&model.UserDataset{},
		&model.UserRole{},
		&model.UserPermission{},
		&model.DynamicConfig{},
		&model.ConfigHistory{},
		&model.Evaluation{},
	}
}

func migrate(db *gorm.DB, contribs []framework.MigrationRegistrar) {
	entities := centralEntities()
	entities = append(entities, framework.FlattenMigrations(contribs)...)
	if err := db.AutoMigrate(entities...); err != nil {
		logrus.Fatalf("Failed to migrate database: %v", err)
	}

	createDetectorViews(db)
}

func addDetectorJoins(query *gorm.DB) *gorm.DB {
	return query.
		Joins(`JOIN (
            SELECT 
                e.id,
                c.id AS algorithm_id,
                e.datapack_id,
                ROW_NUMBER() OVER (
                    PARTITION BY c.id, e.datapack_id 
                    ORDER BY e.created_at DESC, e.id DESC
                ) as rn
            FROM executions e
            JOIN container_versions cv ON e.algorithm_version_id = cv.id
            JOIN containers c ON c.id = cv.container_id
            WHERE e.state = 2 AND e.status = 1 AND c.id = ?
        ) er_ranked ON fi.id = er_ranked.datapack_id AND er_ranked.rn = 1`, 1).
		Joins("JOIN detector_results dr ON er_ranked.id = dr.execution_id")
}

func createDetectorViews(db *gorm.DB) {
	_ = db.Migrator().DropView("fault_injection_no_issues")
	_ = db.Migrator().DropView("fault_injection_with_issues")

	noIssuesQuery := addDetectorJoins(db.Table("fault_injections fi").
		Select(`DISTINCT 
		fi.id AS datapack_id, 
		fi.name AS name, 
		fi.fault_type AS fault_type,
		fi.category AS category, 
		fi.engine_config AS engine_config, 
		l.label_key as label_key,
		l.label_value as label_value,
		fi.created_at`).
		Joins("LEFT JOIN fault_injection_labels fil ON fil.fault_injection_id = fi.id").
		Joins("LEFT JOIN labels l ON fil.label_id = l.id").
		Group("fi.id, fi.name, fi.fault_type, fi.engine_config, fi.created_at, l.label_key, l.label_value"),
	).Where("dr.issues = '{}' OR dr.issues IS NULL")
	if err := db.Migrator().CreateView("fault_injection_no_issues", gorm.ViewOption{Query: noIssuesQuery}); err != nil {
		logrus.Errorf("failed to create fault_injection_no_issues view: %v", err)
	}

	withIssuesQuery := addDetectorJoins(db.Table("fault_injections fi").
		Select(`DISTINCT 
		fi.id AS datapack_id, 
		fi.name AS name,
		fi.fault_type AS fault_type,
		fi.category AS category, 
		fi.engine_config AS engine_config, 
		l.label_key as label_key,
		l.label_value as label_value,
		fi.created_at, 
		dr.issues, 
		dr.abnormal_avg_duration, 
		dr.normal_avg_duration, 
		dr.abnormal_succ_rate, 
		dr.normal_succ_rate, 
		dr.abnormal_p99, 
		dr.normal_p99`).
		Joins("LEFT JOIN tasks t ON t.id = fi.task_id").
		Joins("LEFT JOIN fault_injection_labels fil ON fil.fault_injection_id = fi.id").
		Joins("LEFT JOIN labels l ON fil.label_id = l.id").
		Group("fi.id, fi.name, fi.fault_type, fi.engine_config, fi.created_at, l.label_key, l.label_value, dr.issues, dr.abnormal_avg_duration, dr.normal_avg_duration, dr.abnormal_succ_rate, dr.normal_succ_rate, dr.abnormal_p99, dr.normal_p99"),
	).Where("dr.issues != '{}' AND dr.issues IS NOT NULL")
	if err := db.Migrator().CreateView("fault_injection_with_issues", gorm.ViewOption{Query: withIssuesQuery}); err != nil {
		logrus.Errorf("failed to create fault_injection_with_issues view: %v", err)
	}
}
