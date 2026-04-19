package team

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "team",
		Entities: []interface{}{
			&model.Team{},
		},
	}
}
