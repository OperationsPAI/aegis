package user

import (
	"aegis/framework"
	"aegis/model"
)

// Migrations owns only the user-identity table. &model.APIKey{}
// logically belongs to module/auth (token exchange + key encryption);
// it stays in central until issue #39 migrates module/auth.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "user",
		Entities: []interface{}{
			&model.User{},
		},
	}
}
