package project

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "project",
		Entities: []interface{}{
			&model.Project{},
			&model.ProjectLabel{},
			&model.UserProjectWorkspace{},
			// UserProject role-grant collapsed into UserScopedRole; migration owned by rbac module.
		},
	}
}
