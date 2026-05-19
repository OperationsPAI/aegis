package chaoshooks

import (
	"fmt"

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
// (id, kind, terminal_status) to (id, kind) per design §11 step 4.
// Idempotent if no stickiness breaches exist; pre-flight rejects if
// they do — the operator must reconcile manually before the schema
// can be tightened, because a blind ALTER would hit Duplicate entry
// and leave the table in an undefined state.
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

	type dup struct {
		ID   string
		Kind string
		N    int64
	}
	var dups []dup
	if err := db.Raw(`
		SELECT id, kind, COUNT(*) AS n
		FROM chaos_hook_submissions
		GROUP BY id, kind
		HAVING COUNT(*) > 1
	`).Scan(&dups).Error; err != nil {
		return fmt.Errorf("chaos_hook_submissions: pre-flight check failed: %w", err)
	}
	if len(dups) > 0 {
		return fmt.Errorf(
			"chaos_hook_submissions: %d (id, kind) groups have multiple terminal_status rows; "+
				"ADR-0006 stickiness was breached. Manually reconcile before redeploying "+
				"(keep one row per (id, kind), e.g. earliest submitted_at)",
			len(dups),
		)
	}

	return db.Exec(
		"ALTER TABLE chaos_hook_submissions DROP PRIMARY KEY, ADD PRIMARY KEY (id, kind)",
	).Error
}
