package team

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "team",
		Entities: []interface{}{
			&model.Team{},
		},
	}
}
