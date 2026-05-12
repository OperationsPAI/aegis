package widget

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
)

var PermWidgetReadAll = consts.PermissionRule{
	Resource: consts.ResourceName("widget"),
	Action:   consts.ActionRead,
	Scope:    consts.ScopeAll,
}

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "widget",
		Rules: []consts.PermissionRule{
			PermWidgetReadAll,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "widget",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {PermWidgetReadAll},
			consts.RoleUser:  {PermWidgetReadAll},
		},
	}
}
