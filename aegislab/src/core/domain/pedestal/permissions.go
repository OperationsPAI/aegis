package pedestal

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
)

// Permissions registers the pedestal runtime permission rules. These gate the
// admin /pedestals endpoints (helm install / list / restart / uninstall) and
// are intentionally distinct from PermSystem* (which gates chaossystem row
// CRUD): the pedestal runtime ops have larger blast radius (`helm uninstall`
// in any namespace visible to the kubeconfig) and audit clarity benefits
// from a dedicated resource name.
func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "pedestal",
		Rules: []consts.PermissionRule{
			consts.PermPedestalRead,
			consts.PermPedestalManage,
		},
	}
}

// RoleGrants seeds the new pedestal permissions onto the same roles that
// historically had PermSystemManage so existing admin users keep their
// install/restart/uninstall access without an out-of-band grant migration.
func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "pedestal",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleAdmin: {
				consts.PermPedestalRead,
				consts.PermPedestalManage,
			},
		},
	}
}
