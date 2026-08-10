package task

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "task",
		Entities: []interface{}{&model.Task{}},
	}
}
