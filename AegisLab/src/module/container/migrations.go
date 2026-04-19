package container

import (
	"aegis/framework"
	"aegis/model"
)

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "container",
		Entities: []interface{}{
			&model.Container{},
			&model.ContainerVersion{},
			&model.HelmConfig{},
			&model.ParameterConfig{},
			&model.ContainerLabel{},
			&model.ContainerVersionEnvVar{},
			&model.HelmConfigValue{},
			&model.UserContainer{},
		},
	}
}
