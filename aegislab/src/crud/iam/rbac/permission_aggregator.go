package rbac

import (
	"aegis/platform/consts"
	"aegis/platform/framework"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

// PermissionAggregatorParams is the fx-group input for AggregatePermissions.
// Both groups are collected at startup from module-provided registrars.
type PermissionAggregatorParams struct {
	fx.In

	Permissions []framework.PermissionRegistrar `group:"permissions"`
	RoleGrants  []framework.RoleGrantsRegistrar `group:"role_grants"`
}

// AggregatePermissions merges module-contributed role grants into
// `consts.SystemRolePermissions` so downstream readers (the producer
// bootstrap and the permission seeder in service/initialization) pick
// them up alongside the central grants.
//
// This is an fx.Invoke that runs at startup before
// service/initialization.InitializeProducer, because fx orders side-
// effecting Invokes by dependency. Since the bootstrap initializer
// consumes *gorm.DB (populated in infra/db) and this Invoke has no DB
// dependency, fx runs this first by default; we additionally guarantee
// ordering because producer.go reads the map directly on the same
// goroutine after fx.Start returns.
//
// Phase 3 is COEXISTENCE: each Phase 4 PR removes its module's rule
// list from `consts.SystemRolePermissions` (for role grants) and its
// `Perm*` vars (for the catalog) — and adds them via registrars here.
func AggregatePermissions(p PermissionAggregatorParams) {
	// Merge role-grants contributions into the consts map. Duplicates
	// are preserved (SystemRolePermissions itself allows them).
	for _, contrib := range p.RoleGrants {
		for role, rules := range contrib.Grants {
			consts.SystemRolePermissions[role] = append(consts.SystemRolePermissions[role], rules...)
		}
	}

	// Permission-only contributions don't feed any global var today;
	// they exist so Phase 4 modules can declare the PermissionRule
	// catalog locally. The catalog is discoverable via fx for future
	// tooling (e.g. a permission-docs generator).
	permTotal := 0
	for _, c := range p.Permissions {
		permTotal += len(c.Rules)
	}

	if len(p.RoleGrants) > 0 || len(p.Permissions) > 0 {
		logrus.WithFields(logrus.Fields{
			"role_grant_modules": len(p.RoleGrants),
			"permission_modules": len(p.Permissions),
			"permission_rules":   permTotal,
		}).Info("aggregated module-contributed permissions")
	}
}
