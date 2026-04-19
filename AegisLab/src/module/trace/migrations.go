package trace

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "trace",
		Entities: []interface{}{
			&model.Trace{},
		},
	}
}
