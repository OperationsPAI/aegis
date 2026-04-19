package framework

import "aegis/consts"

// PermissionRegistrar is what a module contributes for permission
// self-registration.
//
// A module provides it with:
//
//	fx.Provide(
//	    fx.Annotate(module.Permissions, fx.ResultTags(`group:"permissions"`)),
//	)
//
// The aggregator collects `[]PermissionRegistrar` at startup and flattens
// `.Rules` to the effective permission catalog. During Phase 3 this
// catalog coexists with `consts.SystemRolePermissions`; modules migrated
// in Phase 4 MOVE their rule lists out of consts/system.go into their
// own module/<name>/permissions.go file.
type PermissionRegistrar struct {
	Module string
	Rules  []consts.PermissionRule
}

// RoleGrantsRegistrar is what a module contributes when its permissions
// need to be granted to a set of system roles by default.
//
// A module provides it with:
//
//	fx.Provide(
//	    fx.Annotate(module.RoleGrants, fx.ResultTags(`group:"role_grants"`)),
//	)
//
// Multiple modules can contribute grants to the same RoleName; the
// aggregator unions them.
type RoleGrantsRegistrar struct {
	Module string
	Grants map[consts.RoleName][]consts.PermissionRule
}

// MergeRoleGrants folds a set of RoleGrantsRegistrar entries into `base`,
// appending to each RoleName's rule list. `base` is mutated and returned.
// If `base` is nil a fresh map is allocated.
//
// Exported so that Phase 4 tests and the aggregation site (module/rbac or
// consts initializer) can verify behavior.
func MergeRoleGrants(base map[consts.RoleName][]consts.PermissionRule, contribs []RoleGrantsRegistrar) map[consts.RoleName][]consts.PermissionRule {
	if base == nil {
		base = make(map[consts.RoleName][]consts.PermissionRule)
	}
	for _, contrib := range contribs {
		for role, rules := range contrib.Grants {
			base[role] = append(base[role], rules...)
		}
	}
	return base
}

// FlattenPermissions returns the union of every contributed rule.
// Duplicates are preserved; callers that need a deduped list should do
// that themselves (today consts.SystemRolePermissions allows duplicates).
func FlattenPermissions(contribs []PermissionRegistrar) []consts.PermissionRule {
	total := 0
	for _, c := range contribs {
		total += len(c.Rules)
	}
	out := make([]consts.PermissionRule, 0, total)
	for _, c := range contribs {
		out = append(out, c.Rules...)
	}
	return out
}
