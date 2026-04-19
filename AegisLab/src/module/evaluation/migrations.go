package evaluation

import (
	"aegis/framework"
	"aegis/model"
)

// Migrations declares the evaluation-owned table for AutoMigrate.
// Evaluation does not own detector/granularity result rows or any
// join tables, so only model.Evaluation is registered here.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "evaluation",
		Entities: []interface{}{&model.Evaluation{}},
	}
}
