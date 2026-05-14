package user

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceCreateUserValidationError(t *testing.T) {
	service := NewService(nil, nil)

	_, err := service.CreateUser(t.Context(), &CreateUserReq{
		Username: "demo",
		Email:    "demo@example.com",
		Password: "short",
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "validation failed")
	require.ErrorContains(t, err, "password must be at least 8 characters")
}
