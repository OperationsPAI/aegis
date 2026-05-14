package system

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

// Migrations owns the system module's persistence layer. ConfigLabel belongs
// here because it is the join table for DynamicConfig, which is system-owned.
// The retired `systems` table used to be part of this set (issue #75).
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "system",
		Entities: []interface{}{
			&model.AuditLog{},
			&model.DynamicConfig{},
			&model.ConfigHistory{},
			&model.ConfigLabel{},
			&model.SystemMetadata{},
		},
	}
}
