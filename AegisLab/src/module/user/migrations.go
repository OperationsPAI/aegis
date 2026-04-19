package user

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "user",
		Entities: []interface{}{
			&model.User{},
			&model.APIKey{},
		},
	}
}
