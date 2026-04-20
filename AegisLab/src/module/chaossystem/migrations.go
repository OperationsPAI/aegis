package chaossystem

import (
	"aegis/framework"
	"aegis/model"
)

// Migrations keeps SystemMetadata under this module. The systems table itself
// has been retired (issue #75); etcd + dynamic_configs are the new source of
// truth for the per-system runtime knobs.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "chaossystem",
		Entities: []interface{}{
			&model.SystemMetadata{},
		},
	}
}
