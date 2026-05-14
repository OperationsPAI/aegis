package pedestal

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
)

// Permissions is intentionally empty for pedestal. Its HTTP endpoints reuse
// container-version permissions instead of defining pedestal-specific Perm*
// rules in consts/system.go.
func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "pedestal",
		Rules:  []consts.PermissionRule{},
	}
}

// RoleGrants is intentionally empty because pedestal does not own any default
// system-role grants; access is inherited from the container/container-version
// rules that already guard these endpoints.
func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "pedestal",
		Grants: map[consts.RoleName][]consts.PermissionRule{},
	}
}
