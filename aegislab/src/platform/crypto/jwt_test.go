package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func resolver() PublicKeyResolver {
	return func(_ string) (*rsa.PublicKey, error) {
		return &testRSAKey.PublicKey, nil
	}
}

func TestGenerateAndParseUserToken(t *testing.T) {
	tok, _, err := GenerateToken(42, "alice", "alice@x.com", true, false, []string{"admin"}, testRSAKey, "test-kid")
	require.NoError(t, err)

	claims, err := ParseToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, 42, claims.UserID)
	require.Equal(t, "alice", claims.Username)
	require.Equal(t, []string{"admin"}, claims.Roles)
	require.True(t, claims.IsActive)
	require.Equal(t, "user", claims.AuthType)
}

func TestGenerateAPIKeyToken(t *testing.T) {
	tok, _, err := GenerateAPIKeyToken(7, "bob", "b@x", true, true, []string{"admin"}, 99, []string{"read"}, testRSAKey, "test-kid")
	require.NoError(t, err)

	claims, err := ParseToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, "api_key", claims.AuthType)
	require.Equal(t, 99, claims.APIKeyID)
	require.Equal(t, []string{"read"}, claims.APIKeyScopes)
}

func TestParseToken_RejectsInactiveUser(t *testing.T) {
	tok, _, err := GenerateToken(42, "alice", "alice@x.com", false, false, nil, testRSAKey, "test-kid")
	require.NoError(t, err)

	_, err = ParseToken(tok, resolver())
	require.Error(t, err)
}

func TestServiceToken(t *testing.T) {
	tok, _, err := GenerateServiceToken("task-xyz", testRSAKey, "test-kid")
	require.NoError(t, err)

	claims, err := ParseServiceToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, "task-xyz", claims.TaskID)
}

func TestServiceAccountToken_Roundtrip(t *testing.T) {
	scopes := []string{"chaos.inject.write", "chaos.webhook.write"}
	tok, exp, err := GenerateServiceAccountToken("chaos-service", scopes, 24*time.Hour, testRSAKey, "test-kid")
	require.NoError(t, err)
	require.NotEmpty(t, tok)
	require.True(t, exp.After(time.Now()))

	claims, err := ParseServiceAccountToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, "chaos-service", claims.Subject)
	require.Equal(t, "rcabench-sa", claims.Issuer)
	require.Equal(t, "service_account", claims.AuthType)
	require.Equal(t, scopes, claims.Scopes)

	// Service-account parser must reject a user token (wrong issuer).
	userTok, _, err := GenerateToken(1, "u", "u@x", true, false, nil, testRSAKey, "test-kid")
	require.NoError(t, err)
	_, err = ParseServiceAccountToken(userTok, resolver())
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestParseToken_RejectsWrongKey(t *testing.T) {
	tok, _, err := GenerateToken(1, "a", "a@x", true, false, nil, testRSAKey, "test-kid")
	require.NoError(t, err)

	_, err = ParseToken(tok, func(_ string) (*rsa.PublicKey, error) {
		// return a different key
		other, _ := generateOtherKey()
		return &other.PublicKey, nil
	})
	require.Error(t, err)
}

func TestParseToken_ResolverErrorRejects(t *testing.T) {
	tok, _, err := GenerateToken(1, "a", "a@x", true, false, nil, testRSAKey, "test-kid")
	require.NoError(t, err)

	_, err = ParseToken(tok, func(_ string) (*rsa.PublicKey, error) {
		return nil, errExample
	})
	require.Error(t, err)
}

var errExample = errors.New("boom")

func generateOtherKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}
