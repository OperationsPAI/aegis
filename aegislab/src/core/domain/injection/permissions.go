package injection

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
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
			consts.PermInjectionReadAll,
			consts.PermInjectionUpdateAll,
			consts.PermInjectionDeleteAll,
			consts.PermInjectionCloneAll,
			consts.PermInjectionDownloadAll,
			consts.PermInjectionUploadAll,
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
				consts.PermInjectionReadAll,
				consts.PermInjectionUpdateAll,
				consts.PermInjectionDeleteAll,
				consts.PermInjectionCloneAll,
				consts.PermInjectionDownloadAll,
				consts.PermInjectionUploadAll,
			},
			consts.RoleProjectAdmin: {
				consts.PermInjectionReadAll,
				consts.PermInjectionDownloadAll,
				consts.PermInjectionCloneAll,
			},
			consts.RoleProjectDataDeveloper: {
				consts.PermInjectionReadAll,
				consts.PermInjectionDownloadAll,
			},
			consts.RoleProjectViewer: {
				consts.PermInjectionReadAll,
			},
		},
	}
}
