package app_test

import (
	"os"
	"testing"

	"aegis/platform/crypto"
)

func TestMain(m *testing.M) {
	_ = os.Setenv(crypto.JWTSecretEnvVar, "test-jwt-secret-please-ignore-not-for-prod")
	if err := crypto.InitJWTSecret(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
