package chaosprune

import "aegis/platform/framework"

// Permissions exists for Phase 4 consistency. The prune endpoint is guarded by
// the global RequireSystemAdmin middleware; no module-specific rules.
func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "chaosprune",
		Rules:  nil,
	}
}

// RoleGrants exists for Phase 4 consistency.
func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "chaosprune",
		Grants: nil,
	}
}
