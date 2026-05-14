package ratelimiter

import "aegis/platform/framework"

// Permissions exists for Phase 4 consistency. The ratelimiter module does not
// define module-specific PermissionRule constants today; its mutating routes are
// guarded by the global RequireSystemAdmin middleware.
func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "ratelimiter",
		Rules:  nil,
	}
}

// RoleGrants exists for Phase 4 consistency. There are no ratelimiter-specific
// entries to migrate from consts.SystemRolePermissions.
func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "ratelimiter",
		Grants: nil,
	}
}
