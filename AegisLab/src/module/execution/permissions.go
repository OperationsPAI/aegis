package execution

import (
	"aegis/consts"
	"aegis/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "execution",
		Rules: []consts.PermissionRule{
			consts.PermExecutionReadProject,
			consts.PermExecutionCreateProject,
			consts.PermExecutionUpdateProject,
			consts.PermExecutionDeleteProject,
			consts.PermExecutionExecuteProject,
			consts.PermExecutionStopProject,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "execution",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermExecutionReadProject,
				consts.PermExecutionCreateProject,
				consts.PermExecutionUpdateProject,
				consts.PermExecutionDeleteProject,
				consts.PermExecutionExecuteProject,
				consts.PermExecutionStopProject,
			},
			consts.RoleProjectAdmin: {
				consts.PermExecutionReadProject,
				consts.PermExecutionExecuteProject,
			},
			consts.RoleProjectAlgoDeveloper: {
				consts.PermExecutionReadProject,
				consts.PermExecutionExecuteProject,
			},
			consts.RoleProjectViewer: {
				consts.PermExecutionReadProject,
			},
		},
	}
}
