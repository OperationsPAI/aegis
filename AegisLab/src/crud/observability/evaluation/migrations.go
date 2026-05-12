package evaluation

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

// Migrations declares the evaluation-owned tables for AutoMigrate.
// EvaluationRolloutStats must precede EvaluationSample: EvaluationSample's
// table (evaluation_data) carries the FK `id -> evaluation_rollout_stats.id`,
// so the parent table must exist before AutoMigrate creates the child.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "evaluation",
		Entities: []interface{}{
			&model.Evaluation{},
			&model.EvaluationRolloutStats{},
			&model.EvaluationSample{},
		},
	}
}
