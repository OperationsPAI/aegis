package label

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
)

// Permissions is the label module's PermissionRegistrar. It contributes
// every PermissionRule the module needs so other modules / the admin
// UI can enumerate them via the `group:"permissions"` fx-group.
//
// These rules still appear in consts/system.go's Perm* vars during
// Phase 3 coexistence — the framework aggregator only *adds* discovered
// rules to a catalog; it does not dedupe against consts. Phase 5 (or a
// later cleanup) will remove the duplicate Perm* vars from consts.
func Permissions() framework.PermissionRegistrar {
	return framework.PermissionRegistrar{
		Module: "label",
		Rules: []consts.PermissionRule{
			consts.PermLabelReadAll,
			consts.PermLabelCreateAll,
			consts.PermLabelCreateOwn,
			consts.PermLabelUpdateAll,
			consts.PermLabelDeleteAll,
		},
	}
}

// RoleGrants is the label module's RoleGrantsRegistrar. It contributes
// the same role→rule associations that used to live in
// consts.SystemRolePermissions so they persist through the Phase 3
// aggregation.
//
// The central map in consts/system.go has this module's entries
// REMOVED (in the same commit that lands this file) — the aggregator
// in module/rbac re-adds them at startup via fx.Invoke. If you skip
// rbac.Module in a test, these grants won't be applied.
func RoleGrants() framework.RoleGrantsRegistrar {
	return framework.RoleGrantsRegistrar{
		Module: "label",
		Grants: map[consts.RoleName][]consts.PermissionRule{
			// Admin gets the full label management suite (was lines
			// 420-424 of consts/system.go before migration).
			consts.RoleAdmin: {
				consts.PermLabelReadAll,
				consts.PermLabelCreateAll,
				consts.PermLabelUpdateAll,
				consts.PermLabelDeleteAll,
			},
			// Regular users can create labels they own and read all
			// labels (was in the RoleUser block).
			consts.RoleUser: {
				consts.PermLabelCreateOwn,
				consts.PermLabelReadAll,
			},
		},
	}
}
