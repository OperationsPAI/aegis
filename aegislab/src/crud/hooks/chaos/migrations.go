package chaoshooks

import "aegis/platform/framework"

func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "hooks.chaos",
		Entities: []any{&HookSubmission{}},
	}
}
