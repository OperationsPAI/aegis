package systemmetric

import "aegis/framework"

// Migrations is intentionally empty. The systemmetric module stores its
// rolling metric history in Redis and does not own any SQL tables, so
// there is no MigrationRegistrar contribution to wire into AutoMigrate.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "systemmetric",
		Entities: []interface{}{},
	}
}
