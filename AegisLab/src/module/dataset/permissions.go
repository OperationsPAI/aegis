package dataset

import (
	"aegis/consts"
	"aegis/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "dataset",
		Rules: []consts.PermissionRule{
			consts.PermDatasetReadAll,
			consts.PermDatasetReadTeam,
			consts.PermDatasetCreateAll,
			consts.PermDatasetCreateTeam,
			consts.PermDatasetCreateOwn,
			consts.PermDatasetUpdateAll,
			consts.PermDatasetUpdateTeam,
			consts.PermDatasetDeleteAll,
			consts.PermDatasetManageAll,
			consts.PermDatasetVersionReadAll,
			consts.PermDatasetVersionReadTeam,
			consts.PermDatasetVersionCreateAll,
			consts.PermDatasetVersionCreateTeam,
			consts.PermDatasetVersionUpdateAll,
			consts.PermDatasetVersionUpdateTeam,
			consts.PermDatasetVersionDeleteAll,
			consts.PermDatasetVersionManageAll,
			consts.PermDatasetVersionDownloadAll,
			consts.PermDatasetVersionDownloadTeam,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "dataset",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermDatasetReadAll,
				consts.PermDatasetCreateAll,
				consts.PermDatasetUpdateAll,
				consts.PermDatasetDeleteAll,
				consts.PermDatasetManageAll,
				consts.PermDatasetVersionReadAll,
				consts.PermDatasetVersionCreateAll,
				consts.PermDatasetVersionUpdateAll,
				consts.PermDatasetVersionDeleteAll,
				consts.PermDatasetVersionDownloadAll,
			},
			consts.RoleUser: {
				consts.PermDatasetCreateOwn,
			},
			consts.RoleDatasetAdmin: {
				consts.PermDatasetReadAll,
				consts.PermDatasetCreateAll,
				consts.PermDatasetUpdateAll,
				consts.PermDatasetDeleteAll,
				consts.PermDatasetManageAll,
				consts.PermDatasetVersionReadAll,
				consts.PermDatasetVersionCreateAll,
				consts.PermDatasetVersionUpdateAll,
				consts.PermDatasetVersionDeleteAll,
				consts.PermDatasetVersionManageAll,
				consts.PermDatasetVersionDownloadAll,
			},
			consts.RoleDatasetDeveloper: {
				consts.PermDatasetReadTeam,
				consts.PermDatasetCreateTeam,
				consts.PermDatasetUpdateTeam,
				consts.PermDatasetVersionReadTeam,
				consts.PermDatasetVersionCreateTeam,
				consts.PermDatasetVersionUpdateTeam,
				consts.PermDatasetVersionDownloadTeam,
			},
			consts.RoleDatasetViewer: {
				consts.PermDatasetReadAll,
				consts.PermDatasetVersionReadAll,
				consts.PermDatasetVersionDownloadAll,
			},
		},
	}
}
