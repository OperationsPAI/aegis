package llmeval

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "llmeval",
		Entities: []interface{}{
			// EvaluationRolloutStats first: EvaluationSample's table
			// (evaluation_data) carries the FK `id -> evaluation_rollout_stats.id`,
			// so the parent table must exist before AutoMigrate creates the child.
			&model.EvaluationRolloutStats{},
			&model.EvaluationSample{},
		},
	}
}
