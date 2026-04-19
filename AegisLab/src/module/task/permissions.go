package task

import (
	"aegis/consts"
	"aegis/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "task",
		Rules: []consts.PermissionRule{
			consts.PermTaskReadAll,
			consts.PermTaskCreateAll,
			consts.PermTaskUpdateAll,
			consts.PermTaskDeleteAll,
			consts.PermTaskExecuteAll,
			consts.PermTaskStopAll,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "task",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermTaskReadAll,
				consts.PermTaskCreateAll,
				consts.PermTaskUpdateAll,
				consts.PermTaskDeleteAll,
				consts.PermTaskExecuteAll,
				consts.PermTaskStopAll,
			},
		},
	}
}
