package system

import (
	"aegis/framework"
	"aegis/model"
)

// Migrations owns the system module's persistence layer. ConfigLabel belongs
// here because it is the join table for DynamicConfig, which is system-owned.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "system",
		Entities: []interface{}{
			&model.AuditLog{},
			&model.DynamicConfig{},
			&model.ConfigHistory{},
			&model.ConfigLabel{},
			&model.System{},
			&model.SystemMetadata{},
		},
	}
}
