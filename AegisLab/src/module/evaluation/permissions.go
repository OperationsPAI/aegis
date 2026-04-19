package evaluation

import (
	"aegis/consts"
	"aegis/framework"
)

// Permissions documents that the evaluation module currently contributes
// no system PermissionRule entries. Its endpoints rely on JWT/API-key
// scope middleware rather than RBAC PermissionRule checks.
func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "evaluation",
		Rules:  []consts.PermissionRule{},
	}
}

// RoleGrants mirrors Permissions: evaluation has no entries in
// consts.SystemRolePermissions to migrate during Phase 4, so this
// module contributes an empty grant set.
func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "evaluation",
		Grants: map[consts.RoleName][]consts.PermissionRule{},
	}
}
