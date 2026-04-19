package app_test

import (
	"os"
	"testing"

	"aegis/utils"
)

func TestMain(m *testing.M) {
	_ = os.Setenv(utils.JWTSecretEnvVar, "test-jwt-secret-please-ignore-not-for-prod")
	if err := utils.InitJWTSecret(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
