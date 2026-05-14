package sso

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"aegis/platform/consts"
)

// verifyPKCE checks that a code_verifier matches the stored challenge per
// RFC 7636. `plain` compares directly; `S256` compares base64url(sha256(v)).
func verifyPKCE(challenge, method, verifier string) bool {
	switch method {
	case "", consts.PKCEMethodPlain:
		return challenge == verifier
	case consts.PKCEMethodS256:
		sum := sha256.Sum256([]byte(verifier))
		return challenge == base64.RawURLEncoding.EncodeToString(sum[:])
	default:
		return false
	}
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
