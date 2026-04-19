package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEncryptDecryptAPIKeySecret_V1RoundTrip verifies that a secret encrypted
// through the v1 envelope round-trips cleanly and starts with the v1 version
// byte once base64-decoded.
func TestEncryptDecryptAPIKeySecret_V1RoundTrip(t *testing.T) {
	const secret = "hunter2-hunter2-hunter2"

	ciphertext, err := EncryptAPIKeySecret(secret)
	require.NoError(t, err)
	require.NotEmpty(t, ciphertext)

	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(raw), 1+12+16)
	require.Equal(t, byte(apiKeyCiphertextV1), raw[0], "envelope must start with v1 marker")

	plain, err := DecryptAPIKeySecret(ciphertext)
	require.NoError(t, err)
	require.Equal(t, secret, plain)
}

// TestDecryptAPIKeySecret_V0Backcompat constructs a ciphertext using the
// legacy sha256(JWTSecret) KEK (no version byte) and confirms the decrypt
// path falls back to the v0 code.
func TestDecryptAPIKeySecret_V0Backcompat(t *testing.T) {
	const secret = "legacy-secret-value"

	key := apiKeyCryptoKeyV0()
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)

	nonce := make([]byte, gcm.NonceSize())
	_, err = rand.Read(nonce)
	require.NoError(t, err)

	// Legacy envelope layout: nonce || ciphertext+tag, no version prefix.
	sealed := gcm.Seal(nonce, nonce, []byte(secret), nil)
	ciphertext := base64.StdEncoding.EncodeToString(sealed)

	plain, err := DecryptAPIKeySecret(ciphertext)
	require.NoError(t, err)
	require.Equal(t, secret, plain)
}

// TestV1KeyDiffersFromV0 asserts that the v1 HKDF-derived key is not equal
// to the legacy sha256(JWTSecret) key — otherwise the cryptographic domain
// separation promised by the rotation would not hold.
func TestV1KeyDiffersFromV0(t *testing.T) {
	v1, err := apiKeyCryptoKeyV1()
	require.NoError(t, err)
	v0 := apiKeyCryptoKeyV0()

	require.Len(t, v1, 32)
	require.Len(t, v0, 32)
	require.NotEqual(t, v0, v1, "HKDF-derived key must differ from sha256(JWTSecret)")
}
