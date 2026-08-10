package system

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "system",
		Rules: []consts.PermissionRule{
			consts.PermSystemRead,
			consts.PermSystemConfigure,
			consts.PermSystemManage,
			consts.PermAuditRead,
			consts.PermAuditAudit,
			consts.PermConfigurationRead,
			consts.PermConfigurationUpdate,
			consts.PermConfigurationConfigure,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "system",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermSystemRead,
				consts.PermSystemConfigure,
				consts.PermSystemManage,
				consts.PermAuditRead,
				consts.PermAuditAudit,
				consts.PermConfigurationRead,
				consts.PermConfigurationUpdate,
				consts.PermConfigurationConfigure,
			},
		},
	}
}
