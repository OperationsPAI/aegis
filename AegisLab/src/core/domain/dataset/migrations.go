package dataset

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "dataset",
		Entities: []interface{}{
			&model.Dataset{},
			&model.DatasetVersion{},
			&model.DatasetLabel{},
			&model.DatasetVersionInjection{},
			// UserDataset collapsed into UserScopedRole; migration owned by rbac module.
		},
	}
}
