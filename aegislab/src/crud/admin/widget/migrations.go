package widget

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "widget",
		Entities: []interface{}{&model.Widget{}},
	}
}
