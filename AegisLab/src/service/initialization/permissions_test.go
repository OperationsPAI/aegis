package initialization

import (
	"testing"

	"aegis/consts"
	"aegis/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newPermTestDB spins up an in-memory sqlite DB with the RBAC tables
// migrated. SQLite accepts the `GENERATED ALWAYS AS ... VIRTUAL` clause on
// Permission.ActiveName, but since uniqueness there is enforced by the
// MySQL-only computed column, the tests below also seed via Create and rely
// on ON CONFLICT(name) for the upsertPermissions path.
func newPermTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Resource{},
		&model.Permission{},
		&model.Role{},
		&model.RolePermission{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// SQLite doesn't enforce the computed-column uniqueness the same way
	// MySQL does, so add a plain unique index on Permission.Name. This is
	// what upsertPermissions(ON CONFLICT {name}) needs to hit.
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_perm_name ON permissions(name)").Error; err != nil {
		t.Fatalf("index permissions.name: %v", err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_role_name ON roles(name)").Error; err != nil {
		t.Fatalf("index roles.name: %v", err)
	}
	return db
}

// withInjectedPermission saves + restores consts.SystemRolePermissions so
// tests can declare a fresh grant without leaking state.
func withInjectedPermission(t *testing.T, role consts.RoleName, rule consts.PermissionRule) {
	t.Helper()
	original := make(map[consts.RoleName][]consts.PermissionRule, len(consts.SystemRolePermissions))
	for k, v := range consts.SystemRolePermissions {
		original[k] = append([]consts.PermissionRule(nil), v...)
	}
	t.Cleanup(func() {
		consts.SystemRolePermissions = original
	})

	if consts.SystemRolePermissions == nil {
		consts.SystemRolePermissions = map[consts.RoleName][]consts.PermissionRule{}
	}
	consts.SystemRolePermissions[role] = append(consts.SystemRolePermissions[role], rule)
}

// TestReconcileSystemPermissions_FreshDBCreatesPermissionAndLink exercises
// the first scenario from issue #104: permission X and its role_permissions
// link are both absent; running reconcile creates both.
func TestReconcileSystemPermissions_FreshDBCreatesPermissionAndLink(t *testing.T) {
	db := newPermTestDB(t)

	// Inject a new rule on a plain system role (not super_admin — super_admin
	// receives ALL permissions by design and would obscure the link assertion).
	newRule := consts.PermissionRule{
		Resource: consts.ResourceTrace,
		Action:   consts.ActionName("stop"),
		Scope:    consts.ScopeAll,
	}
	withInjectedPermission(t, consts.RoleAdmin, newRule)

	if err := ReconcileSystemPermissions(db); err != nil {
		t.Fatalf("ReconcileSystemPermissions: %v", err)
	}

	var perm model.Permission
	if err := db.Where("name = ?", newRule.String()).First(&perm).Error; err != nil {
		t.Fatalf("new permission %q not inserted: %v", newRule.String(), err)
	}
	if !perm.IsSystem || perm.Status != consts.CommonEnabled {
		t.Fatalf("permission row unexpected: %+v", perm)
	}

	var role model.Role
	if err := db.Where("name = ?", consts.RoleAdmin.String()).First(&role).Error; err != nil {
		t.Fatalf("admin role not created: %v", err)
	}

	var link model.RolePermission
	if err := db.Where("role_id = ? AND permission_id = ?", role.ID, perm.ID).First(&link).Error; err != nil {
		t.Fatalf("role_permissions link missing for admin -> %s: %v", newRule.String(), err)
	}
}

// TestReconcileSystemPermissions_ExistingPermissionBackfillsLink is the
// precise scenario the INSERT IGNORE workaround addressed: the permission
// row was inserted on an earlier boot, but the role_permissions join was
// not. On the next boot, the link must be backfilled.
func TestReconcileSystemPermissions_ExistingPermissionBackfillsLink(t *testing.T) {
	db := newPermTestDB(t)

	newRule := consts.PermissionRule{
		Resource: consts.ResourceTrace,
		Action:   consts.ActionName("cancel"),
		Scope:    consts.ScopeAll,
	}
	withInjectedPermission(t, consts.RoleAdmin, newRule)

	// First boot: both permission + link created.
	if err := ReconcileSystemPermissions(db); err != nil {
		t.Fatalf("first ReconcileSystemPermissions: %v", err)
	}

	// Simulate the bug state: delete the role_permissions link but leave the
	// permission row in place, as if a previous (buggy) boot had inserted
	// only the permission.
	var role model.Role
	if err := db.Where("name = ?", consts.RoleAdmin.String()).First(&role).Error; err != nil {
		t.Fatalf("admin role missing: %v", err)
	}
	var perm model.Permission
	if err := db.Where("name = ?", newRule.String()).First(&perm).Error; err != nil {
		t.Fatalf("permission missing: %v", err)
	}
	if err := db.Where("role_id = ? AND permission_id = ?", role.ID, perm.ID).
		Delete(&model.RolePermission{}).Error; err != nil {
		t.Fatalf("delete link for bug simulation: %v", err)
	}

	var count int64
	db.Model(&model.RolePermission{}).
		Where("role_id = ? AND permission_id = ?", role.ID, perm.ID).
		Count(&count)
	if count != 0 {
		t.Fatalf("precondition: expected 0 link rows, got %d", count)
	}

	// Second boot: reconcile must backfill the link WITHOUT duplicating the
	// permission row.
	if err := ReconcileSystemPermissions(db); err != nil {
		t.Fatalf("second ReconcileSystemPermissions: %v", err)
	}

	var linkCount int64
	db.Model(&model.RolePermission{}).
		Where("role_id = ? AND permission_id = ?", role.ID, perm.ID).
		Count(&linkCount)
	if linkCount != 1 {
		t.Fatalf("expected 1 backfilled link, got %d", linkCount)
	}

	var permCount int64
	db.Model(&model.Permission{}).Where("name = ?", newRule.String()).Count(&permCount)
	if permCount != 1 {
		t.Fatalf("expected exactly 1 permission row, got %d (duplicate from non-idempotent upsert?)", permCount)
	}
}

// TestReconcileSystemPermissions_IdempotentOnRerun guards against the
// reconciler doing writes that accumulate rows across boots.
func TestReconcileSystemPermissions_IdempotentOnRerun(t *testing.T) {
	db := newPermTestDB(t)

	if err := ReconcileSystemPermissions(db); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	var permsBefore, rolesBefore, linksBefore int64
	db.Model(&model.Permission{}).Count(&permsBefore)
	db.Model(&model.Role{}).Count(&rolesBefore)
	db.Model(&model.RolePermission{}).Count(&linksBefore)

	if err := ReconcileSystemPermissions(db); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	var permsAfter, rolesAfter, linksAfter int64
	db.Model(&model.Permission{}).Count(&permsAfter)
	db.Model(&model.Role{}).Count(&rolesAfter)
	db.Model(&model.RolePermission{}).Count(&linksAfter)

	if permsAfter != permsBefore {
		t.Fatalf("permissions grew on rerun: %d -> %d", permsBefore, permsAfter)
	}
	if rolesAfter != rolesBefore {
		t.Fatalf("roles grew on rerun: %d -> %d", rolesBefore, rolesAfter)
	}
	if linksAfter != linksBefore {
		t.Fatalf("role_permissions grew on rerun: %d -> %d", linksBefore, linksAfter)
	}
}
