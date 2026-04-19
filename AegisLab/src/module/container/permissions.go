package container

import (
	"aegis/consts"
	"aegis/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "container",
		Rules: []consts.PermissionRule{
			consts.PermContainerReadAll,
			consts.PermContainerReadTeam,
			consts.PermContainerCreateAll,
			consts.PermContainerCreateTeam,
			consts.PermContainerCreateOwn,
			consts.PermContainerUpdateAll,
			consts.PermContainerUpdateTeam,
			consts.PermContainerDeleteAll,
			consts.PermContainerManageAll,
			consts.PermContainerExecuteAll,
			consts.PermContainerExecuteTeam,
			consts.PermContainerVersionReadAll,
			consts.PermContainerVersionReadTeam,
			consts.PermContainerVersionCreateAll,
			consts.PermContainerVersionCreateTeam,
			consts.PermContainerVersionUpdateAll,
			consts.PermContainerVersionUpdateTeam,
			consts.PermContainerVersionDeleteAll,
			consts.PermContainerVersionManageAll,
			consts.PermContainerVersionUploadAll,
			consts.PermContainerVersionUploadTeam,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "container",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermContainerReadAll,
				consts.PermContainerCreateAll,
				consts.PermContainerUpdateAll,
				consts.PermContainerDeleteAll,
				consts.PermContainerManageAll,
				consts.PermContainerExecuteAll,
				consts.PermContainerVersionReadAll,
				consts.PermContainerVersionCreateAll,
				consts.PermContainerVersionUpdateAll,
				consts.PermContainerVersionDeleteAll,
				consts.PermContainerVersionUploadAll,
			},
			consts.RoleUser: {
				consts.PermContainerCreateOwn,
			},
			consts.RoleContainerAdmin: {
				consts.PermContainerReadAll,
				consts.PermContainerCreateAll,
				consts.PermContainerUpdateAll,
				consts.PermContainerDeleteAll,
				consts.PermContainerManageAll,
				consts.PermContainerExecuteAll,
				consts.PermContainerVersionReadAll,
				consts.PermContainerVersionCreateAll,
				consts.PermContainerVersionUpdateAll,
				consts.PermContainerVersionDeleteAll,
				consts.PermContainerVersionManageAll,
				consts.PermContainerVersionUploadAll,
			},
			consts.RoleContainerDeveloper: {
				consts.PermContainerReadTeam,
				consts.PermContainerCreateTeam,
				consts.PermContainerUpdateTeam,
				consts.PermContainerExecuteTeam,
				consts.PermContainerVersionReadTeam,
				consts.PermContainerVersionCreateTeam,
				consts.PermContainerVersionUpdateTeam,
				consts.PermContainerVersionUploadTeam,
			},
			consts.RoleContainerViewer: {
				consts.PermContainerReadAll,
				consts.PermContainerVersionReadAll,
			},
		},
	}
}
