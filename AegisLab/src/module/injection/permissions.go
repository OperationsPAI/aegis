package injection

import (
	"aegis/consts"
	"aegis/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "injection",
		Rules: []consts.PermissionRule{
			consts.PermInjectionReadProject,
			consts.PermInjectionCreateProject,
			consts.PermInjectionUpdateProject,
			consts.PermInjectionDeleteProject,
			consts.PermInjectionExecuteProject,
			consts.PermInjectionCloneProject,
			consts.PermInjectionDownloadProject,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "injection",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermInjectionReadProject,
				consts.PermInjectionCreateProject,
				consts.PermInjectionUpdateProject,
				consts.PermInjectionDeleteProject,
				consts.PermInjectionExecuteProject,
				consts.PermInjectionCloneProject,
				consts.PermInjectionDownloadProject,
			},
			consts.RoleProjectAdmin: {
				consts.PermInjectionReadProject,
				consts.PermInjectionExecuteProject,
			},
			consts.RoleProjectDataDeveloper: {
				consts.PermInjectionReadProject,
				consts.PermInjectionExecuteProject,
			},
			consts.RoleProjectViewer: {
				consts.PermInjectionReadProject,
			},
		},
	}
}
