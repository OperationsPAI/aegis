package trace

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "trace",
		Entities: []interface{}{
			&model.Trace{},
		},
	}
}
