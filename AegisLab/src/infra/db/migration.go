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
		// &model.Label{} migrated to module/label/migrations.go (Phase 3
		// reference migration). Future Phase 4 PRs drop their entities
		// the same way -- remove from this slice, add a
		// framework.MigrationRegistrar in the owning module.
		// &model.User{} migrated to module/user/migrations.go (Phase 4).
		// &model.APIKey{} migrated to module/auth/migrations.go (Phase 4,
		// issue #39).
		&model.AuditLog{},
		// &model.Task{} migrated to module/task/migrations.go (Phase 4).
		// &model.FaultInjection{} migrated to module/injection/migrations.go
		// (Phase 4).
		// &model.Execution{} migrated to module/execution/migrations.go
		// (Phase 4).
		// &model.DetectorResult{} and &model.GranularityResult{} migrated to
		// module/execution/migrations.go (Phase 4).
		// &model.DatasetLabel{} migrated to module/dataset/migrations.go
		// (Phase 4).
		&model.ProjectLabel{},
		// &model.DatasetVersionInjection{} migrated to
		// module/dataset/migrations.go (Phase 4).
		// &model.FaultInjectionLabel{} migrated to
		// module/injection/migrations.go (Phase 4).
		// &model.ExecutionInjectionLabel{} migrated to
		// module/execution/migrations.go (Phase 4).
		// &model.UserDataset{} migrated to module/dataset/migrations.go
		// (Phase 4).
		&model.ConfigLabel{},
		&model.UserRole{},
		&model.UserPermission{},
		&model.UserTeam{},
		&model.DynamicConfig{},
		&model.ConfigHistory{},
		// &model.Evaluation{} migrated to module/evaluation/migrations.go
		// (Phase 4).
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

	noIssuesQuery := db.Table("fault_injections fi").
		Select("fi.*").
		Joins("JOIN datapacks dp ON fi.datapack_id = dp.id").
		Where("dp.algorithm_id = ?", 1)

	if err := noIssuesQuery.
		Not("EXISTS (?)", addDetectorJoins(db.Table("fault_injections fi2").
			Select("1").
			Where("fi2.id = fi.id")).Where("dr.result = ?", 1)).
		Not("EXISTS (?)", addDetectorJoins(db.Table("fault_injections fi2").
			Select("1").
			Where("fi2.id = fi.id")).Where("dr.result = ?", 3)).
		Where("fi.finished_at IS NOT NULL").
		Migrator().CreateView("fault_injection_no_issues", gorm.ViewOption{Query: noIssuesQuery}); err != nil {
		logrus.Warnf("Failed to create view fault_injection_no_issues: %v", err)
	}

	withIssuesQuery := db.Table("fault_injections fi").
		Select("fi.*").
		Joins("JOIN datapacks dp ON fi.datapack_id = dp.id").
		Where("dp.algorithm_id = ?", 1)

	if err := withIssuesQuery.
		Where("EXISTS (?)", addDetectorJoins(db.Table("fault_injections fi2").
			Select("1").
			Where("fi2.id = fi.id")).Where("dr.result = ?", 1)).
		Not("EXISTS (?)", addDetectorJoins(db.Table("fault_injections fi2").
			Select("1").
			Where("fi2.id = fi.id")).Where("dr.result = ?", 3)).
		Where("fi.finished_at IS NOT NULL").
		Migrator().CreateView("fault_injection_with_issues", gorm.ViewOption{Query: withIssuesQuery}); err != nil {
		logrus.Warnf("Failed to create view fault_injection_with_issues: %v", err)
	}
}
