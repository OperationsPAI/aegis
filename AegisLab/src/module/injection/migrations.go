package injection

import (
	"aegis/framework"
	"aegis/model"
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
