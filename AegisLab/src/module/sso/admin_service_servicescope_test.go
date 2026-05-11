package sso

import (
	"context"
	"regexp"
	"testing"

	"aegis/consts"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// expectFindRoleByName matches the FindRoleByName query used by the
// AdminService resolveRole helper.
func expectFindRoleByName(mock sqlmock.Sqlmock, name string, roleID int, isSystem bool) {
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT * FROM `roles` WHERE name = ? AND status >= ? ORDER BY `roles`.`id` LIMIT ?")).
		WithArgs(name, consts.CommonDisabled, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "is_system", "status"}).
			AddRow(roleID, name, isSystem, consts.CommonEnabled))
}

// TestGrantScopedRoleServiceAdminOK: service admin for "yourservice" grants
// the same service_admin role on their own service — allowed.
func TestGrantScopedRoleServiceAdminOK(t *testing.T) {
	svc, mock, cleanup := newAdminService(t)
	defer cleanup()

	expectFindRoleByName(mock, "service_admin", 9, true)

	// AssignScopedRole queries existing row first.
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT * FROM `user_scoped_roles` WHERE user_id = ? AND role_id = ? AND scope_type = ? AND scope_id = ? ORDER BY `user_scoped_roles`.`id` LIMIT ?")).
		WithArgs(42, 9, consts.ScopeTypeService, "yourservice", 1).
		WillReturnError(gorm.ErrRecordNotFound)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `user_scoped_roles`")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	resp, err := svc.GrantScopedRoleForAdmin(context.Background(), &GrantReq{
		UserID:    42,
		Role:      "service_admin",
		ScopeType: consts.ScopeTypeService,
		ScopeID:   "yourservice",
	}, []string{"yourservice"}, false)
	require.NoError(t, err)
	require.True(t, resp.Granted)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGrantScopedRoleServiceAdminWrongService: service admin for "yourservice"
// tries to grant role on "otherservice" — 403.
func TestGrantScopedRoleServiceAdminWrongService(t *testing.T) {
	svc, mock, cleanup := newAdminService(t)
	defer cleanup()

	expectFindRoleByName(mock, "service_admin", 9, true)

	_, err := svc.GrantScopedRoleForAdmin(context.Background(), &GrantReq{
		UserID:    42,
		Role:      "service_admin",
		ScopeType: consts.ScopeTypeService,
		ScopeID:   "otherservice",
	}, []string{"yourservice"}, false)
	require.Error(t, err)
	require.ErrorIs(t, err, consts.ErrPermissionDenied)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGrantScopedRoleServiceAdminCrossService: granting a generic-scope role
// whose permissions span "otherservice" — 403.
func TestGrantScopedRoleServiceAdminCrossService(t *testing.T) {
	svc, mock, cleanup := newAdminService(t)
	defer cleanup()

	expectFindRoleByName(mock, "project_admin", 5, true)

	// ListRolePermissionServices.
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT DISTINCT p.service FROM permissions p JOIN role_permissions rp ON rp.permission_id = p.id WHERE rp.role_id = ? AND p.status >= ?")).
		WithArgs(5, consts.CommonDisabled).
		WillReturnRows(sqlmock.NewRows([]string{"service"}).AddRow("aegis").AddRow("otherservice"))

	_, err := svc.GrantScopedRoleForAdmin(context.Background(), &GrantReq{
		UserID:    42,
		Role:      "project_admin",
		ScopeType: consts.ScopeTypeProject,
		ScopeID:   "proj-1",
	}, []string{"aegis"}, false)
	require.Error(t, err)
	require.ErrorIs(t, err, consts.ErrPermissionDenied)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGrantScopedRoleGlobalAdminAlwaysWins: global admin grants any role,
// no scope check, no role-permissions lookup.
func TestGrantScopedRoleGlobalAdminAlwaysWins(t *testing.T) {
	svc, mock, cleanup := newAdminService(t)
	defer cleanup()

	expectFindRoleByName(mock, "project_admin", 5, true)

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT * FROM `user_scoped_roles` WHERE user_id = ? AND role_id = ? AND scope_type = ? AND scope_id = ? ORDER BY `user_scoped_roles`.`id` LIMIT ?")).
		WithArgs(42, 5, consts.ScopeTypeProject, "proj-1", 1).
		WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `user_scoped_roles`")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	resp, err := svc.GrantScopedRoleForAdmin(context.Background(), &GrantReq{
		UserID:    42,
		Role:      "project_admin",
		ScopeType: consts.ScopeTypeProject,
		ScopeID:   "proj-1",
	}, nil, true)
	require.NoError(t, err)
	require.True(t, resp.Granted)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRegisterPermissionsServiceAdminWrongService: service admin tries to
// register a permission for a service they don't admin — 403.
func TestRegisterPermissionsServiceAdminWrongService(t *testing.T) {
	svc, _, cleanup := newAdminService(t)
	defer cleanup()

	_, err := svc.RegisterPermissionsForAdmin(context.Background(),
		&RegisterPermissionsReq{
			Service: "otherservice",
			Permissions: []PermissionSpec{
				{Name: "x.read", DisplayName: "Read X"},
			},
		}, []string{"yourservice"}, false)
	require.Error(t, err)
	require.ErrorIs(t, err, consts.ErrPermissionDenied)
}

// TestAdminContextMayActOnService covers the package-level gate helpers
// without needing a DB — pure logic.
func TestAdminContextMayActOnService(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ctx  *AdminContext
		svc  string
		ok   bool
	}{
		{"nil", nil, "any", false},
		{"global", &AdminContext{IsGlobalAdmin: true}, "any", true},
		{"service-token", &AdminContext{ServiceTokenFor: "x"}, "any", true},
		{"service-admin-match", &AdminContext{ServiceAdminFor: []string{"a", "b"}}, "b", true},
		{"service-admin-miss", &AdminContext{ServiceAdminFor: []string{"a"}}, "b", false},
		{"empty", &AdminContext{}, "any", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, c.ok, c.ctx.MayActOnService(c.svc))
		})
	}
}

