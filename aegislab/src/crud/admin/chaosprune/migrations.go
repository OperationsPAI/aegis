package chaosprune

import "aegis/platform/framework"

// Migrations exists for Phase 4 consistency. The chaosprune module owns no
// database tables; it reads model.Task and reaps k8s objects via the dynamic
// client.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "chaosprune",
		Entities: nil,
	}
}
