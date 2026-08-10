package widget

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "widget",
		Rules:  []consts.PermissionRule{},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "widget",
		Grants: map[consts.RoleName][]consts.PermissionRule{},
	}
}
