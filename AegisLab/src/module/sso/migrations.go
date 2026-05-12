package sso

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "sso",
		Entities: []interface{}{&model.OIDCClient{}},
	}
}
