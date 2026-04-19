package user

import (
	"aegis/consts"
	"aegis/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "user",
		Rules: []consts.PermissionRule{
			consts.PermUserReadAll,
			consts.PermUserCreateAll,
			consts.PermUserUpdateAll,
			consts.PermUserDeleteAll,
			consts.PermUserAssignAll,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "user",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermUserReadAll,
				consts.PermUserCreateAll,
				consts.PermUserUpdateAll,
				consts.PermUserDeleteAll,
				consts.PermUserAssignAll,
			},
		},
	}
}
