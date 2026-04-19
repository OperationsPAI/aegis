package evaluation

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "evaluation",
		Entities: []interface{}{&model.Evaluation{}},
	}
}
