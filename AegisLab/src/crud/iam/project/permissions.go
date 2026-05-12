package project

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "project",
		Rules: []consts.PermissionRule{
			consts.PermProjectReadAll,
			consts.PermProjectReadTeam,
			consts.PermProjectReadOwn,
			consts.PermProjectCreateTeam,
			consts.PermProjectCreateOwn,
			consts.PermProjectUpdateAll,
			consts.PermProjectUpdateOwn,
			consts.PermProjectDeleteAll,
			consts.PermProjectDeleteOwn,
			consts.PermProjectManageAll,
			consts.PermProjectManageOwn,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "project",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermProjectReadAll,
				consts.PermProjectUpdateAll,
				consts.PermProjectDeleteAll,
				consts.PermProjectManageAll,
			},
			consts.RoleUser: {
				consts.PermProjectCreateOwn,
				consts.PermProjectReadOwn,
			},
			consts.RoleProjectAdmin: {
				consts.PermProjectReadOwn,
				consts.PermProjectUpdateOwn,
				consts.PermProjectDeleteOwn,
				consts.PermProjectManageOwn,
				consts.PermInjectionReadProject,
				consts.PermInjectionExecuteProject,
				consts.PermExecutionReadProject,
				consts.PermExecutionExecuteProject,
			},
			consts.RoleProjectAlgoDeveloper: {
				consts.PermProjectReadOwn,
				consts.PermExecutionReadProject,
				consts.PermExecutionExecuteProject,
			},
			consts.RoleProjectDataDeveloper: {
				consts.PermProjectReadOwn,
				consts.PermInjectionReadProject,
				consts.PermInjectionExecuteProject,
			},
			consts.RoleProjectViewer: {
				consts.PermProjectReadOwn,
				consts.PermInjectionReadProject,
				consts.PermExecutionReadProject,
			},
		},
	}
}
