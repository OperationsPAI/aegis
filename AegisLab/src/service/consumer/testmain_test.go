package consumer

import (
	"crypto/rand"
	"crypto/rsa"
	"os"
	"testing"

	"aegis/infra/jwtkeys"
)

func TestMain(m *testing.M) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	signerRef.Store(&jwtkeys.Signer{PrivateKey: k, Kid: "test-kid"})
	os.Exit(m.Run())
}
