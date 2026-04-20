package widget

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "widget",
		Entities: []interface{}{&model.Widget{}},
	}
}
