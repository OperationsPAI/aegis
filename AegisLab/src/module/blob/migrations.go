package blob

import "aegis/framework"

// Migrations registers the blob_objects table.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "blob",
		Entities: []any{&ObjectRecord{}},
	}
}
