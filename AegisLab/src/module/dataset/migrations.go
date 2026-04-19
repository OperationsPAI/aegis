package dataset

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "dataset",
		Entities: []interface{}{
			&model.Dataset{},
			&model.DatasetVersion{},
			&model.DatasetLabel{},
			&model.DatasetVersionInjection{},
			&model.UserDataset{},
		},
	}
}
