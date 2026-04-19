package team

import (
	"aegis/consts"
	"aegis/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "team",
		Rules: []consts.PermissionRule{
			consts.PermTeamReadAll,
			consts.PermTeamReadTeam,
			consts.PermTeamCreateAll,
			consts.PermTeamUpdateAll,
			consts.PermTeamUpdateTeam,
			consts.PermTeamDeleteAll,
			consts.PermTeamManageAll,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "team",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermTeamReadAll,
				consts.PermTeamCreateAll,
				consts.PermTeamUpdateAll,
				consts.PermTeamDeleteAll,
				consts.PermTeamManageAll,
			},
			consts.RoleTeamAdmin: {
				consts.PermTeamReadAll,
				consts.PermTeamCreateAll,
				consts.PermTeamUpdateAll,
				consts.PermTeamDeleteAll,
				consts.PermTeamManageAll,
			},
			consts.RoleTeamMember: {
				consts.PermTeamReadTeam,
				consts.PermTeamUpdateTeam,
			},
			consts.RoleTeamViewer: {
				consts.PermTeamReadTeam,
			},
		},
	}
}
