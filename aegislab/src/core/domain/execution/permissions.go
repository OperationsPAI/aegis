package execution

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
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
			consts.PermExecutionReadAll,
			consts.PermExecutionUpdateAll,
			consts.PermExecutionDeleteAll,
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
				consts.PermExecutionReadAll,
				consts.PermExecutionUpdateAll,
				consts.PermExecutionDeleteAll,
			},
			consts.RoleProjectAdmin: {
				consts.PermExecutionReadProject,
				consts.PermExecutionExecuteProject,
				consts.PermExecutionReadAll,
			},
			consts.RoleProjectAlgoDeveloper: {
				consts.PermExecutionReadProject,
				consts.PermExecutionExecuteProject,
				consts.PermExecutionReadAll,
			},
			consts.RoleProjectViewer: {
				consts.PermExecutionReadProject,
				consts.PermExecutionReadAll,
			},
		},
	}
}
