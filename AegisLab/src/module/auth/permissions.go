package auth

import (
	"aegis/consts"
	"aegis/framework"
)

// Auth currently owns authentication and API-key flows but no standalone
// consts.Perm* rules. The empty registrars keep the Phase 4 module shape
// consistent and let future auth permissions land without touching module.go.
func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "auth",
		Rules:  []consts.PermissionRule{},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "auth",
		Grants: map[consts.RoleName][]consts.PermissionRule{},
	}
}
