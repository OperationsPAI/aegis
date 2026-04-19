package systemmetric

import (
	"aegis/consts"
	"aegis/framework"
)

// Permissions is intentionally empty. The systemmetric module reuses the
// system module's existing PermSystemRead rule for its admin endpoints
// and does not define any module-owned permission constants in
// consts/system.go.
func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "systemmetric",
		Rules:  []consts.PermissionRule{},
	}
}

// RoleGrants is intentionally empty for the same reason as Permissions:
// no systemmetric-owned permission rules exist to grant to roles.
func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "systemmetric",
		Grants: map[consts.RoleName][]consts.PermissionRule{},
	}
}
