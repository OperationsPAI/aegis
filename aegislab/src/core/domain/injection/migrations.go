package injection

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "injection",
		Entities: []interface{}{
			&model.FaultInjection{},
			&model.FaultInjectionLabel{},
		},
	}
}
