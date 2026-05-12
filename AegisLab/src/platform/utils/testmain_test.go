package utils

import (
	"crypto/rand"
	"crypto/rsa"
	"os"
	"testing"
)

var testRSAKey *rsa.PrivateKey

// TestMain primes the API-key KEK secret used by access_key_crypto.go and
// generates an ephemeral RSA keypair the JWT tests sign with.
func TestMain(m *testing.M) {
	_ = os.Setenv(JWTSecretEnvVar, "test-jwt-secret-please-ignore-not-for-prod")
	if err := InitJWTSecret(); err != nil {
		panic(err)
	}
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	testRSAKey = k
	os.Exit(m.Run())
}
