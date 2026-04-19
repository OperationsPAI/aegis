package execution

import (
	"aegis/framework"
	"aegis/model"
)

// Migrations is the execution module's MigrationRegistrar. It owns the
// execution result tables themselves; the execution/label join table is
// still owned by the label side during Phase 4 coexistence.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "execution",
		Entities: []interface{}{
			&model.Execution{},
			&model.DetectorResult{},
			&model.GranularityResult{},
		},
	}
}
