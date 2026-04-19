package execution

import (
	"aegis/framework"
	"aegis/model"
)

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
