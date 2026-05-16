package pages

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
)

// Pages introduces its own resource name + rules. The catalog is the
// source of truth at SSO; this registrar is the contribution side.
const ResourcePages consts.ResourceName = "pages"

var (
	PermPagesReadOwn    = consts.PermissionRule{Resource: ResourcePages, Action: consts.ActionRead, Scope: consts.ScopeOwn}
	PermPagesWriteOwn   = consts.PermissionRule{Resource: ResourcePages, Action: consts.ActionUpdate, Scope: consts.ScopeOwn}
	PermPagesManageAll  = consts.PermissionRule{Resource: ResourcePages, Action: consts.ActionManage, Scope: consts.ScopeAll}
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
