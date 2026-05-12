package auth

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "auth",
		Entities: []interface{}{
			&model.APIKey{},
		},
	}
}
