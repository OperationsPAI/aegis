package initialization

import (
	"fmt"

	"aegis/consts"
	"aegis/model"
	"aegis/utils"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ReconcileSystemPermissions upserts the RBAC baseline — system resources,
// permissions, system roles, and role-permission bindings — derived from
// consts.SystemRolePermissions. It runs on EVERY startup (issue #104), so a
// newly-added permission (via consts/system.go or a module-contributed
// RoleGrants registrar aggregated by rbac.AggregatePermissions) shows up in
// the DB without requiring a fresh install.
//
// Idempotency contract:
//   - Resources: ON CONFLICT(name) DO NOTHING (no writable columns).
//   - Permissions: ON CONFLICT(name) DO NOTHING.
//   - Roles: ON CONFLICT(name) DO NOTHING.
//   - RolePermissions: ON CONFLICT(role_id, permission_id) DO NOTHING.
//
// This is important because the permissions-table upsert alone does not
// guarantee a role_permissions link exists — if the permissions row was
// created on an earlier boot but role_permissions was not, the link would
// never be backfilled. The workaround for that gap was a manual
// `INSERT IGNORE INTO role_permissions`. This function folds that step into
// every startup.
//
// Callers must ensure rbac.AggregatePermissions has already merged module-
// contributed grants into consts.SystemRolePermissions. In the production
// fx graph that holds because AggregatePermissions is an fx.Invoke that
// runs before any lifecycle OnStart hook.
func ReconcileSystemPermissions(db *gorm.DB) error {
	resources := systemResources()

	return withOptimizedDBSettings(db, func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			store := newBootstrapStore(tx)
			return reconcileSystemPermissionsTx(store, resources)
		})
	})
}

// systemResources is the canonical list of RBAC resources. It mirrors the
// list used by initializeProducer (first-boot seed) so both paths produce
// identical rows.
//
// The hardcoded list covers the core platform resources. Module-contributed
// resources (registered via fx-group RoleGrantsRegistrar and merged into
// consts.SystemRolePermissions by rbac.AggregatePermissions) are unioned in
// by extendWithReferencedResources. This prevents the "resource X referenced
// by permission rule not found" boot-time crash when a new module adds a
// permission on a resource that the canonical list doesn't know about yet.
func systemResources() []model.Resource {
	resources := []model.Resource{
		{Name: consts.ResourceSystem, Type: consts.ResourceTypeSystem, Category: consts.ResourceCategorySystem},
		{Name: consts.ResourceAudit, Type: consts.ResourceTypeTable, Category: consts.ResourceCategorySystem},
		{Name: consts.ResourceConfiguration, Type: consts.ResourceTypeTable, Category: consts.ResourceCategorySystem},
		{Name: consts.ResourceContainer, Type: consts.ResourceTypeTable, Category: consts.ResourceCategoryAsset},
		{Name: consts.ResourceContainerVersion, Type: consts.ResourceTypeTable, Category: consts.ResourceCategoryAsset},
		{Name: consts.ResourceDataset, Type: consts.ResourceTypeTable, Category: consts.ResourceCategoryAsset},
		{Name: consts.ResourceDatasetVersion, Type: consts.ResourceTypeTable, Category: consts.ResourceCategoryAsset},
		{Name: consts.ResourceProject, Type: consts.ResourceTypeTable, Category: consts.ResourceCategoryPlatform},
		{Name: consts.ResourceTeam, Type: consts.ResourceTypeTable, Category: consts.ResourceCategoryPlatform},
		{Name: consts.ResourceLabel, Type: consts.ResourceTypeTable, Category: consts.ResourceCategoryAsset},
		{Name: consts.ResourceUser, Type: consts.ResourceTypeTable, Category: consts.ResourceCategorySystem},
		{Name: consts.ResourceRole, Type: consts.ResourceTypeTable, Category: consts.ResourceCategorySystem},
		{Name: consts.ResourcePermission, Type: consts.ResourceTypeTable, Category: consts.ResourceCategorySystem},
		{Name: consts.ResourceTask, Type: consts.ResourceTypeTable, Category: consts.ResourceCategoryChaos},
		{Name: consts.ResourceTrace, Type: consts.ResourceTypeTable, Category: consts.ResourceCategoryChaos},
		{Name: consts.ResourceInjection, Type: consts.ResourceTypeTable, Category: consts.ResourceCategoryChaos},
		{Name: consts.ResourceExecution, Type: consts.ResourceTypeTable, Category: consts.ResourceCategoryChaos},
	}
	resources = extendWithReferencedResources(resources)
	for i := range resources {
		resources[i].DisplayName = consts.GetResourceDisplayName(resources[i].Name)
	}
	return resources
}

// extendWithReferencedResources ensures every resource referenced by a
// permission rule in consts.SystemRolePermissions has a corresponding
// Resource row. Module-contributed rules (via fx-group PermissionRegistrar /
// RoleGrantsRegistrar) may reference resources that the hardcoded base list
// doesn't know about — widget is a concrete example (#104 follow-up).
// Unknown resources get safe defaults: Type=Table, Category=Asset.
func extendWithReferencedResources(base []model.Resource) []model.Resource {
	known := make(map[consts.ResourceName]struct{}, len(base))
	for _, r := range base {
		known[r.Name] = struct{}{}
	}
	seen := make(map[consts.ResourceName]struct{})
	for _, rules := range consts.SystemRolePermissions {
		for _, rule := range rules {
			if _, ok := known[rule.Resource]; ok {
				continue
			}
			if _, ok := seen[rule.Resource]; ok {
				continue
			}
			seen[rule.Resource] = struct{}{}
			base = append(base, model.Resource{
				Name:     rule.Resource,
				Type:     consts.ResourceTypeTable,
				Category: consts.ResourceCategoryAsset,
			})
		}
	}
	return base
}

// systemRoles is the set of system roles derived from
// consts.SystemRoleDisplayNames. Kept as a helper so
// reconcileSystemPermissionsTx and initializeProducer agree on the list.
func systemRoles() []model.Role {
	roles := make([]model.Role, 0, len(consts.SystemRoleDisplayNames))
	for role, displayName := range consts.SystemRoleDisplayNames {
		roles = append(roles, model.Role{
			Name:        role.String(),
			DisplayName: displayName,
			IsSystem:    true,
			Status:      consts.CommonEnabled,
		})
	}
	return roles
}

// reconcileSystemPermissionsTx performs the idempotent reconciliation inside
// an existing transaction. Used both by ReconcileSystemPermissions (the
// every-boot entry point) and by initializeProducer (the first-boot seeder).
//
// Logs newly-inserted permissions and role grants for operability: in the
// most common case (no new permissions added) all counts are zero and the
// function emits a single debug-level line; when a new permission is being
// picked up for the first time, the Info log makes the event greppable
// without reading SQL.
func reconcileSystemPermissionsTx(store *bootstrapStore, resources []model.Resource) error {
	// --- Resources ---------------------------------------------------------
	beforeResources, err := store.listResourcesByNames(resourceNames(resources))
	if err != nil {
		return fmt.Errorf("list resources (before): %w", err)
	}
	if err := store.upsertResources(resources); err != nil {
		return fmt.Errorf("upsert resources: %w", err)
	}
	afterResources, err := store.listResourcesByNames(resourceNames(resources))
	if err != nil {
		return fmt.Errorf("list resources (after): %w", err)
	}
	if len(afterResources) != len(resources) {
		return fmt.Errorf("resource reconcile mismatch: want %d rows, got %d", len(resources), len(afterResources))
	}

	resourceIDMap := make(map[consts.ResourceName]int, len(afterResources))
	resourceMap := make(map[consts.ResourceName]*model.Resource, len(afterResources))
	for i := range afterResources {
		resourceIDMap[afterResources[i].Name] = afterResources[i].ID
		resourceMap[afterResources[i].Name] = &afterResources[i]
	}

	// Parent links (container_version -> container, dataset_version -> dataset)
	// — upserted only when the post-state row is missing the parent.
	if cv, ok := resourceMap[consts.ResourceContainerVersion]; ok {
		cv.ParentID = utils.IntPtr(resourceIDMap[consts.ResourceContainer])
	}
	if dv, ok := resourceMap[consts.ResourceDatasetVersion]; ok {
		dv.ParentID = utils.IntPtr(resourceIDMap[consts.ResourceDataset])
	}
	parentUpdates := []model.Resource{}
	if cv, ok := resourceMap[consts.ResourceContainerVersion]; ok {
		parentUpdates = append(parentUpdates, *cv)
	}
	if dv, ok := resourceMap[consts.ResourceDatasetVersion]; ok {
		parentUpdates = append(parentUpdates, *dv)
	}
	if len(parentUpdates) > 0 {
		if err := store.upsertResources(parentUpdates); err != nil {
			return fmt.Errorf("upsert resource parents: %w", err)
		}
	}

	newResources := 0
	existingNames := make(map[consts.ResourceName]struct{}, len(beforeResources))
	for _, r := range beforeResources {
		existingNames[r.Name] = struct{}{}
	}
	for _, r := range resources {
		if _, ok := existingNames[r.Name]; !ok {
			newResources++
		}
	}

	// --- Permissions -------------------------------------------------------
	uniquePermissions := make(map[string]permMeta)
	for _, permissionRules := range consts.SystemRolePermissions {
		for _, rule := range permissionRules {
			resourceID, ok := resourceIDMap[rule.Resource]
			if !ok {
				return fmt.Errorf("resource %s referenced by permission rule not found", rule.Resource)
			}
			key := rule.String()
			if _, exists := uniquePermissions[key]; !exists {
				uniquePermissions[key] = permMeta{
					action:        rule.Action,
					resourceID:    resourceID,
					resourceName:  rule.Resource,
					resourceScope: rule.Scope,
				}
			}
		}
	}

	permNames := make([]string, 0, len(uniquePermissions))
	for k := range uniquePermissions {
		permNames = append(permNames, k)
	}
	existingPerms, err := store.listPermissionsByNames(permNames)
	if err != nil {
		return fmt.Errorf("list permissions (before): %w", err)
	}
	existingPermNames := make(map[string]struct{}, len(existingPerms))
	for _, p := range existingPerms {
		existingPermNames[p.Name] = struct{}{}
	}

	permissionsToCreate := make([]model.Permission, 0, len(uniquePermissions))
	newPermissionNames := make([]string, 0)
	for permName, permData := range uniquePermissions {
		permissionsToCreate = append(permissionsToCreate, model.Permission{
			Name:        permName,
			DisplayName: permData.String(),
			Action:      permData.action,
			Service:     consts.ServiceAegis,
			ScopeType:   scopeTypeForResourceScope(permData.resourceScope),
			IsSystem:    true,
			Status:      consts.CommonEnabled,
		})
		if _, ok := existingPermNames[permName]; !ok {
			newPermissionNames = append(newPermissionNames, permName)
		}
	}
	if len(permissionsToCreate) > 0 {
		if err := store.upsertPermissions(permissionsToCreate); err != nil {
			return fmt.Errorf("upsert permissions: %w", err)
		}
	}

	// --- Roles -------------------------------------------------------------
	roles := systemRoles()
	if err := store.upsertRoles(roles); err != nil {
		return fmt.Errorf("upsert roles: %w", err)
	}

	// --- Role permissions --------------------------------------------------
	// Crucially, this step runs even for pre-existing permission rows — that
	// is the bug fix for issue #104. A new permission added to an existing
	// role's grant list must be linked here even if the permissions row was
	// inserted by an earlier boot.
	newLinks, err := assignSystemRolePermissionsReturningNew(store)
	if err != nil {
		return fmt.Errorf("assign role permissions: %w", err)
	}

	if newResources > 0 || len(newPermissionNames) > 0 || newLinks > 0 {
		logrus.WithFields(logrus.Fields{
			"new_resources":       newResources,
			"new_permissions":     len(newPermissionNames),
			"new_role_grants":     newLinks,
			"permission_examples": truncateStrings(newPermissionNames, 8),
		}).Info("rbac: reconciled system permissions")
	} else {
		logrus.Debug("rbac: system permissions already in sync")
	}

	return nil
}

func resourceNames(resources []model.Resource) []consts.ResourceName {
	out := make([]consts.ResourceName, 0, len(resources))
	for _, r := range resources {
		out = append(out, r.Name)
	}
	return out
}

func truncateStrings(s []string, max int) []string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// assignSystemRolePermissionsReturningNew mirrors assignSystemRolePermissions
// but counts how many role_permissions rows did not exist before this call.
// The count is best-effort: we query existing links per role once, then
// compare against the target set. Conflicts in the INSERT are still handled
// by `ON CONFLICT DO NOTHING` as a safety net.
func assignSystemRolePermissionsReturningNew(store *bootstrapStore) (int, error) {
	newLinks := 0
	for roleName, permissionRules := range consts.SystemRolePermissions {
		role, err := store.getRoleByName(roleName.String())
		if err != nil {
			return 0, fmt.Errorf("role %s not found: %w", roleName, err)
		}

		var targetPerms []model.Permission
		if roleName == consts.RoleSuperAdmin {
			targetPerms, err = store.listSystemPermissions()
			if err != nil {
				return 0, fmt.Errorf("list system permissions: %w", err)
			}
		} else {
			permStrs := make([]string, 0, len(permissionRules))
			seen := make(map[string]struct{}, len(permissionRules))
			for _, rule := range permissionRules {
				s := rule.String()
				if _, ok := seen[s]; ok {
					continue
				}
				seen[s] = struct{}{}
				permStrs = append(permStrs, s)
			}
			targetPerms, err = store.listPermissionsByNames(permStrs)
			if err != nil {
				return 0, fmt.Errorf("list permissions for role %s: %w", roleName, err)
			}
		}

		existingLinks, err := store.listRolePermissionsByRole(role.ID)
		if err != nil {
			return 0, fmt.Errorf("list role_permissions for role %s: %w", roleName, err)
		}
		existingSet := make(map[int]struct{}, len(existingLinks))
		for _, l := range existingLinks {
			existingSet[l.PermissionID] = struct{}{}
		}

		toCreate := make([]model.RolePermission, 0, len(targetPerms))
		for _, p := range targetPerms {
			if _, ok := existingSet[p.ID]; ok {
				continue
			}
			toCreate = append(toCreate, model.RolePermission{
				RoleID:       role.ID,
				PermissionID: p.ID,
			})
		}
		if len(toCreate) > 0 {
			if err := store.createRolePermissions(toCreate); err != nil {
				return 0, fmt.Errorf("create role_permissions for role %s: %w", roleName, err)
			}
			newLinks += len(toCreate)
			logrus.WithFields(logrus.Fields{
				"role":      roleName.String(),
				"new_links": len(toCreate),
			}).Info("rbac: linked new permissions to role")
		}
	}
	return newLinks, nil
}

// scopeTypeForResourceScope maps the legacy in-memory ResourceScope (own /
// project / team / all) to the new user_scoped_roles scope_type stored on
// Permission. ScopeAll = global (empty scope_type); project/team/own keep
// their project/team mapping. "own" is a UserPermission-only concept and
// doesn't have a UserScopedRole equivalent, so it stays empty too.
func scopeTypeForResourceScope(s consts.ResourceScope) string {
	switch s {
	case consts.ScopeProject:
		return consts.ScopeTypeProject
	case consts.ScopeTeam:
		return consts.ScopeTypeTeam
	default:
		return ""
	}
}
