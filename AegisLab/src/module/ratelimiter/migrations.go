package ratelimiter

import "aegis/framework"

// Migrations exists for Phase 4 consistency. The ratelimiter module owns no
// database tables; it operates on Redis token buckets and reads model.Task.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "ratelimiter",
		Entities: nil,
	}
}
