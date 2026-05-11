package sso

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func newService(t *testing.T) (*Service, sqlmock.Sqlmock, func()) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	require.NoError(t, err)
	return NewService(NewRepository(db)), mock, func() { _ = sqlDB.Close() }
}

func TestCreateClientValidation(t *testing.T) {
	svc, _, cleanup := newService(t)
	defer cleanup()

	_, err := svc.Create(context.Background(), &CreateClientReq{})
	require.Error(t, err)

	_, err = svc.Create(context.Background(), &CreateClientReq{
		ClientID: "x", Name: "n", Service: "s",
		Grants: []string{"weird_grant"},
	})
	require.Error(t, err)
}

func TestSecretGenerationDistinct(t *testing.T) {
	a, err := generateSecret()
	require.NoError(t, err)
	b, err := generateSecret()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	require.Len(t, a, 64) // hex(32 bytes)
}

func TestHashSecretRoundTrip(t *testing.T) {
	secret, err := generateSecret()
	require.NoError(t, err)
	hash, err := hashSecret(secret)
	require.NoError(t, err)
	require.NotEqual(t, secret, hash)
	require.NotEmpty(t, hash)
}
