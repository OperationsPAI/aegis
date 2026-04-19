package task

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "task",
		Entities: []interface{}{&model.Task{}},
	}
}
