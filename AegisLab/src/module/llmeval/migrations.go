package llmeval

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "llmeval",
		Entities: []interface{}{
			&model.EvaluationSample{},
			&model.EvaluationRolloutStats{},
		},
	}
}
