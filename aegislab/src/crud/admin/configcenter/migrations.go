package configcenter

import "aegis/platform/framework"

// Migrations registers the config_audit table.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "configcenter",
		Entities: []any{&ConfigAudit{}},
	}
}
