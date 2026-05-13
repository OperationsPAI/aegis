package share

import "aegis/platform/framework"

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "share",
		Entities: []any{&ShareLink{}},
	}
}
