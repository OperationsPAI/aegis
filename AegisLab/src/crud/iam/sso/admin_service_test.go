package sso

import (
	"context"
	"regexp"
	"testing"

	"aegis/platform/consts"
	"aegis/crud/iam/rbac"
	"aegis/crud/iam/user"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func newAdminService(t *testing.T) (*AdminService, sqlmock.Sqlmock, func()) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	require.NoError(t, err)

	svc := NewAdminService(user.NewService(user.NewRepository(db), nil), rbac.NewRepository(db))
	return svc, mock, func() { _ = sqlDB.Close() }
}

// TestCheckAllowedGlobal: permission has no scope; user has it through a
// global role grant — should return allowed=true with role:<name>.
func TestCheckAllowedGlobal(t *testing.T) {
	svc, mock, cleanup := newAdminService(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `permissions` WHERE name = ? AND status >= ? ORDER BY `permissions`.`id` LIMIT ?")).
		WithArgs("system.admin.read", consts.CommonDisabled, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "service", "scope_type", "status"}).
			AddRow(7, "system.admin.read", "aegis", "", consts.CommonEnabled))

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT roles.name AS role_name FROM role_permissions rp " +
			"JOIN user_roles ur ON rp.role_id = ur.role_id " +
			"JOIN roles ON roles.id = rp.role_id " +
			"WHERE (ur.user_id = ? AND rp.permission_id = ?) AND roles.status = ? LIMIT ?")).
		WithArgs(42, 7, consts.CommonEnabled, 1).
		WillReturnRows(sqlmock.NewRows([]string{"role_name"}).AddRow("admin"))

	resp, err := svc.Check(context.Background(), &CheckReq{
		UserID:     42,
		Permission: "system.admin.read",
	})
	require.NoError(t, err)
	require.True(t, resp.Allowed)
	require.Equal(t, "role:admin", resp.Reason)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCheckDeniedGlobal: permission exists, no role grant — denied. The
// service-admin fallback (Task #13) also returns no rows.
func TestCheckDeniedGlobal(t *testing.T) {
	svc, mock, cleanup := newAdminService(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `permissions` WHERE name = ? AND status >= ? ORDER BY `permissions`.`id` LIMIT ?")).
		WithArgs("injection.create", consts.CommonDisabled, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "service", "scope_type", "status"}).
			AddRow(11, "injection.create", "aegis", "", consts.CommonEnabled))

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT roles.name AS role_name FROM role_permissions rp " +
			"JOIN user_roles ur ON rp.role_id = ur.role_id " +
			"JOIN roles ON roles.id = rp.role_id " +
			"WHERE (ur.user_id = ? AND rp.permission_id = ?) AND roles.status = ? LIMIT ?")).
		WithArgs(42, 11, consts.CommonEnabled, 1).
		WillReturnRows(sqlmock.NewRows([]string{"role_name"}))

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT roles.name AS role_name FROM role_permissions rp " +
			"JOIN user_scoped_roles usr ON rp.role_id = usr.role_id " +
			"JOIN roles ON roles.id = rp.role_id " +
			"WHERE (usr.user_id = ? AND rp.permission_id = ?) AND usr.scope_type = ? AND usr.scope_id <> '' AND usr.status = ? AND roles.status = ? LIMIT ?")).
		WithArgs(42, 11, consts.ScopeTypeService, consts.CommonEnabled, consts.CommonEnabled, 1).
		WillReturnRows(sqlmock.NewRows([]string{"role_name"}))

	resp, err := svc.Check(context.Background(), &CheckReq{
		UserID:     42,
		Permission: "injection.create",
	})
	require.NoError(t, err)
	require.False(t, resp.Allowed)
	require.Equal(t, "denied", resp.Reason)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCheckPermissionNotFound: unknown permission name → denied (not error).
func TestCheckPermissionNotFound(t *testing.T) {
	svc, mock, cleanup := newAdminService(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `permissions` WHERE name = ? AND status >= ? ORDER BY `permissions`.`id` LIMIT ?")).
		WithArgs("nonexistent.permission", consts.CommonDisabled, 1).
		WillReturnError(gorm.ErrRecordNotFound)

	resp, err := svc.Check(context.Background(), &CheckReq{
		UserID:     42,
		Permission: "nonexistent.permission",
	})
	require.NoError(t, err)
	require.False(t, resp.Allowed)
	require.Equal(t, "denied", resp.Reason)
	require.NoError(t, mock.ExpectationsWereMet())
}
