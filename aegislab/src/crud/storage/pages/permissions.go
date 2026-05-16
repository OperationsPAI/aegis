package pages

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
)

// Pages introduces its own resource name + rules. The catalog is the
// source of truth at SSO; this registrar is the contribution side.
const ResourcePages consts.ResourceName = "pages"

var (
	PermPagesReadOwn   = consts.PermissionRule{Resource: ResourcePages, Action: consts.ActionRead, Scope: consts.ScopeOwn}
	PermPagesWriteOwn  = consts.PermissionRule{Resource: ResourcePages, Action: consts.ActionUpdate, Scope: consts.ScopeOwn}
	PermPagesManageAll = consts.PermissionRule{Resource: ResourcePages, Action: consts.ActionManage, Scope: consts.ScopeAll}

	// pagesReadPerms / pagesWritePerms are the route-level allow-lists.
	// Each follows the inheritance pattern used elsewhere: the broader
	// admin-only `pages:manage:all` also satisfies the read / write
	// requirements so role grants don't need to enumerate everything.
	pagesReadPerms  = []consts.PermissionRule{PermPagesReadOwn, PermPagesWriteOwn, PermPagesManageAll}
	pagesWritePerms = []consts.PermissionRule{PermPagesWriteOwn, PermPagesManageAll}
)

// Permissions advertises the three logical roles the module recognises:
//
//	pages:read   read own pages
//	pages:write  create/modify own pages
//	pages:admin  manage any page (admin-only)
func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "pages",
		Rules: []consts.PermissionRule{
			PermPagesReadOwn,
			PermPagesWriteOwn,
			PermPagesManageAll,
		},
	}
}
