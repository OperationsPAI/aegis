package sdk

import (
	"aegis/consts"
	"aegis/framework"
)

// Permissions intentionally contributes no PermissionRules.
//
// The SDK module's endpoints are gated by API key scopes rather than
// SystemRolePermissions-backed middleware, so there are no sdk-owned Perm*
// constants to migrate out of consts/system.go.
func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "sdk",
		Rules:  []consts.PermissionRule{},
	}
}

// RoleGrants intentionally contributes no role grants for the same reason as
// Permissions: sdk endpoints do not participate in SystemRolePermissions.
func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "sdk",
		Grants: map[consts.RoleName][]consts.PermissionRule{},
	}
}
