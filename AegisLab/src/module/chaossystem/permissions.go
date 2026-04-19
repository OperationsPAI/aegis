package chaossystem

import (
	"aegis/consts"
	"aegis/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "chaossystem",
		Rules: []consts.PermissionRule{
			consts.PermSystemRead,
			consts.PermSystemConfigure,
			consts.PermSystemManage,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "chaossystem",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermSystemRead,
				consts.PermSystemConfigure,
				consts.PermSystemManage,
			},
		},
	}
}
