package chaoshooks

import (
	"aegis/platform/framework"

	"gorm.io/gorm"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:     "hooks.chaos",
		Entities:   []any{&HookSubmission{}},
		PreMigrate: tightenHookSubmissionPK,
	}
}

// tightenHookSubmissionPK narrows chaos_hook_submissions PK from
// (id, kind, terminal_status) to (id, kind) per design §11 step 4. The
// older 3-column PK let a `succeeded` and a later `partial` (or any
// pair of distinct terminals) for the same injection both claim the
// gate — violating the "one downstream per (injection_id, task_type)"
// invariant the upcoming aegis-chaos cutover relies on. Idempotent: it
// is a no-op if the table is absent or already has the 2-column PK.
func tightenHookSubmissionPK(db *gorm.DB) error {
	if db.Dialector.Name() != "mysql" {
		return nil
	}
	mig := db.Migrator()
	if !mig.HasTable("chaos_hook_submissions") {
		return nil
	}
	var pkCols int
	if err := db.Raw(`SELECT COUNT(*) FROM information_schema.key_column_usage
		WHERE table_schema = DATABASE()
		  AND table_name = 'chaos_hook_submissions'
		  AND constraint_name = 'PRIMARY'`).Scan(&pkCols).Error; err != nil {
		return err
	}
	if pkCols <= 2 {
		return nil
	}
	return db.Exec(
		"ALTER TABLE chaos_hook_submissions DROP PRIMARY KEY, ADD PRIMARY KEY (id, kind)",
	).Error
}
