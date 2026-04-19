package auth

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "auth",
		Entities: []interface{}{
			&model.APIKey{},
		},
	}
}
