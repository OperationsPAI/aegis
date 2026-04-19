package chaossystem

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "chaossystem",
		Entities: []interface{}{
			&model.System{},
			&model.SystemMetadata{},
		},
	}
}
