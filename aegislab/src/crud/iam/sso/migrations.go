package sso

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "sso",
		Entities: []interface{}{&model.OIDCClient{}},
	}
}
