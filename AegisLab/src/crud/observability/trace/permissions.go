package trace

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
)

func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "trace",
		Rules: []consts.PermissionRule{
			consts.PermTraceReadAll,
			consts.PermTraceMonitorAll,
			consts.PermTraceStopAll,
		},
	}
}

func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "trace",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermTraceReadAll,
				consts.PermTraceMonitorAll,
				consts.PermTraceStopAll,
			},
		},
	}
}
