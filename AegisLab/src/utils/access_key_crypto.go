package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// API-key ciphertext envelope
//
// Previously this file derived the AES-GCM key used for api_keys.
// key_secret_ciphertext directly as sha256(JWTSecret). That coupling meant a
// JWT signing key leak could also decrypt every stored API key secret.
//
// We now cryptographically decouple the two keys: the API-key KEK is derived
// via HKDF-SHA256 from JWTSecret with a domain separator. Ciphertexts are
// stored in a versioned envelope so pre-rotation (v0) records produced with
// the old sha256(JWTSecret) key can still be decrypted during migration.
//
// Envelope layout for v1 (HKDF-derived key):
//
//	[ version(1B)=0x01 ][ nonce(12B) ][ ciphertext+tag(>=16B) ]
//
// then base64-encoded for storage. Legacy v0 records have no leading version
// byte; they are (nonce || ciphertext+tag) base64-encoded, decrypted with
// the old sha256(JWTSecret) key. Encryption always writes v1.
const (
	apiKeyCiphertextV1 byte = 0x01

	// apiKeyHKDFInfo is the HKDF domain separator. Changing this value
	// invalidates every v1 record; do not change without planning a migration.
	apiKeyHKDFInfo = "aegis-api-key-encryption-v1"
)

// EncryptAPIKeySecret encrypts the given plaintext secret with the v1 HKDF-
// derived key and returns a base64-encoded envelope suitable for storage.
func EncryptAPIKeySecret(secret string) (string, error) {
	key, err := apiKeyCryptoKeyV1()
	if err != nil {
		return "", err
	}

	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	sealed := gcm.Seal(nonce, nonce, []byte(secret), nil)

	// version || nonce || ciphertext+tag
	envelope := make([]byte, 0, 1+len(sealed))
	envelope = append(envelope, apiKeyCiphertextV1)
	envelope = append(envelope, sealed...)
	return base64.StdEncoding.EncodeToString(envelope), nil
}

// DecryptAPIKeySecret decrypts a stored API-key ciphertext. It dispatches on
// the envelope's leading version byte: v1 uses the HKDF key, legacy records
// without a version byte fall back to the pre-rotation sha256(JWTSecret) key
// so existing credentials remain usable after the rotation.
//
// TODO(phase-1): when a v0 record is successfully decrypted, callers could
// opportunistically re-encrypt it with v1 via EncryptAPIKeySecret and persist
// the new ciphertext. The auth Service does not currently expose a save hook
// for this during ExchangeAPIKeyToken; add one in a follow-up so the entire
// table drifts forward naturally instead of relying on RotateAPIKey.
func DecryptAPIKeySecret(ciphertext string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	// A v1 envelope is [version][nonce:12][ciphertext+tag]; the smallest
	// well-formed v1 payload is 1 + 12 + 16 = 29 bytes. If the leading byte
	// matches the version marker AND we have at least that much data, treat
	// it as v1 — otherwise assume legacy v0 and decrypt with the old KEK.
	if len(raw) >= 1+12+16 && raw[0] == apiKeyCiphertextV1 {
		return decryptAPIKeyV1(raw[1:])
	}
	return decryptAPIKeyV0(raw)
}

func decryptAPIKeyV1(payload []byte) (string, error) {
	key, err := apiKeyCryptoKeyV1()
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(payload) < nonceSize {
		return "", fmt.Errorf("ciphertext is too short")
	}
	nonce, enc := payload[:nonceSize], payload[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, enc, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt ciphertext: %w", err)
	}
	return string(plaintext), nil
}

func decryptAPIKeyV0(payload []byte) (string, error) {
	gcm, err := newGCM(apiKeyCryptoKeyV0())
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(payload) < nonceSize {
		return "", fmt.Errorf("ciphertext is too short")
	}
	nonce, enc := payload[:nonceSize], payload[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, enc, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt legacy ciphertext: %w", err)
	}
	return string(plaintext), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GCM: %w", err)
	}
	return gcm, nil
}

func SignAPIKeyRequest(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyAPIKeyRequestSignature(secret, payload, signature string) bool {
	expected := SignAPIKeyRequest(secret, payload)
	return hmac.Equal([]byte(expected), []byte(signature))
}

func SHA256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// apiKeyCryptoKeyV1 derives the 32-byte AES-256 KEK used for new (v1)
// ciphertexts. Using HKDF with a domain separator means a JWT secret leak
// does not directly yield the KEK; an attacker would additionally need
// access to the HKDF inputs and code to recompute it.
func apiKeyCryptoKeyV1() ([]byte, error) {
	if JWTSecret == "" {
		return nil, fmt.Errorf("JWT secret is not initialized; cannot derive api-key KEK")
	}
	// No explicit salt: JWTSecret is already high-entropy by deployment policy
	// (see utils.InitJWTSecret fail-fast checks). If that assumption ever
	// weakens, introduce a deployment-specific salt here.
	h := hkdf.New(sha256.New, []byte(JWTSecret), nil, []byte(apiKeyHKDFInfo))
	key := make([]byte, 32)
	if _, err := io.ReadFull(h, key); err != nil {
		return nil, fmt.Errorf("failed to derive api-key KEK: %w", err)
	}
	return key, nil
}

// apiKeyCryptoKeyV0 reproduces the pre-rotation derivation used before the
// HKDF envelope was introduced. Kept so that records written with the old
// scheme can still be decrypted during migration; do not use for encryption.
func apiKeyCryptoKeyV0() []byte {
	sum := sha256.Sum256([]byte(JWTSecret))
	return sum[:]
}
