package rbac

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "rbac",
		Entities: []interface{}{
			&model.Role{},
			&model.Permission{},
			&model.Resource{},
			&model.RolePermission{},
			&model.UserScopedRole{},
		},
	}
}
