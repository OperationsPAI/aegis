package sdk

import "aegis/framework"

// Migrations documents that the SDK module owns no AutoMigrate-managed tables.
//
// Its read-only GORM models map to external SDK-managed tables (`data` and
// `evaluation_data`), and model comments explicitly forbid adding them to
// AutoMigrate.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "sdk",
		Entities: nil,
	}
}
