package project

import (
	"testing"

	"aegis/platform/authz"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestListProjectsEmptyScopeShortCircuits verifies non-admin callers with no
// visible projects never hit the DB and never see admin rows. Pre-CallerScope
// this degraded to a full project listing.
func TestListProjectsEmptyScopeShortCircuits(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer sqlDB.Close()

	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	require.NoError(t, err)

	svc := NewService(NewRepository(db), nil, nil)
	scope := authz.CallerScope{UserID: 42, IsAdmin: false}

	resp, err := svc.ListProjects(t.Context(), scope, &ListProjectReq{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Empty(t, resp.Items)
	require.NoError(t, mock.ExpectationsWereMet())
}
