package execution

import (
	"aegis/framework"
	"aegis/model"
)

// Migrations is the execution module's MigrationRegistrar. It owns the
// execution result tables and the execution/label join table.
//
// Per AegisLab/CONTRIBUTING.md, join tables migrate with the parent
// entity's module; `ExecutionInjectionLabel` belongs here alongside the
// `Execution` root entity.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "execution",
		Entities: []interface{}{
			&model.Execution{},
			&model.DetectorResult{},
			&model.GranularityResult{},
			&model.ExecutionInjectionLabel{},
		},
	}
}
