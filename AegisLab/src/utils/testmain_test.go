package utils

import (
	"os"
	"testing"
)

// TestMain primes the JWT secret used by jwt.go / access_key_crypto.go so
// that any test which touches JWT-derived cryptography does not panic on an
// unset secret. The value must differ from LegacyJWTSecretDefault or
// InitJWTSecret will reject it.
func TestMain(m *testing.M) {
	_ = os.Setenv(JWTSecretEnvVar, "test-jwt-secret-please-ignore-not-for-prod")
	if err := InitJWTSecret(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
