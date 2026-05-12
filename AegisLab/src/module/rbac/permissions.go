package rbac

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "rbac",
		Rules: []consts.PermissionRule{
			consts.PermRoleReadAll,
			consts.PermRoleCreateAll,
			consts.PermRoleUpdateAll,
			consts.PermRoleDeleteAll,
			consts.PermRoleGrantAll,
			consts.PermRoleRevokeAll,
			consts.PermPermissionReadAll,
			consts.PermPermissionCreateAll,
			consts.PermPermissionUpdateAll,
			consts.PermPermissionDeleteAll,
			consts.PermPermissionManageAll,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "rbac",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermRoleReadAll,
				consts.PermRoleCreateAll,
				consts.PermRoleUpdateAll,
				consts.PermRoleDeleteAll,
				consts.PermRoleGrantAll,
				consts.PermRoleRevokeAll,
				consts.PermPermissionReadAll,
				consts.PermPermissionCreateAll,
				consts.PermPermissionUpdateAll,
				consts.PermPermissionDeleteAll,
				consts.PermPermissionManageAll,
			},
		},
	}
}
