package project

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "project",
		Entities: []interface{}{
			&model.Project{},
			&model.ProjectLabel{},
			&model.UserProject{},
		},
	}
}
