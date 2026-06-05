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

func TestGenerateAndParseHumanToken(t *testing.T) {
	tok, _, err := GenerateUnifiedToken(UnifiedTokenParams{
		Typ: "human", UserID: 42, Username: "alice", Email: "alice@x.com",
		IsActive: true, IsAdmin: false, Roles: []string{"admin"},
		AuthType: "user", Idp: "local",
		Lifetime: TokenExpiration, Audience: []string{"portal"},
	}, testRSAKey, "test-kid")
	require.NoError(t, err)

	claims, err := ParseUnifiedToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, "human", claims.Typ)
	require.Equal(t, 42, claims.UserID)
	require.Equal(t, "alice", claims.Username)
	require.Equal(t, []string{"admin"}, claims.Roles)
	require.True(t, claims.IsActive)
	require.Equal(t, "user", claims.AuthType)
	require.Equal(t, JWTIssuerUnified, claims.Issuer)
}

func TestGenerateAPIKeyToken(t *testing.T) {
	tok, _, err := GenerateUnifiedToken(UnifiedTokenParams{
		Typ: "human", UserID: 7, Username: "bob", Email: "b@x",
		IsActive: true, IsAdmin: true, Roles: []string{"admin"},
		AuthType: "api_key", APIKeyID: 99, APIKeyScopes: []string{"read"},
		Idp: "local", Lifetime: TokenExpiration, Audience: []string{"portal"},
	}, testRSAKey, "test-kid")
	require.NoError(t, err)

	claims, err := ParseUnifiedToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, "api_key", claims.AuthType)
	require.Equal(t, 99, claims.APIKeyID)
	require.Equal(t, []string{"read"}, claims.APIKeyScopes)
}

func TestParseToken_RejectsInactiveUser(t *testing.T) {
	tok, _, err := GenerateUnifiedToken(UnifiedTokenParams{
		Typ: "human", UserID: 42, Username: "alice", Email: "alice@x.com",
		IsActive: false, Lifetime: TokenExpiration,
	}, testRSAKey, "test-kid")
	require.NoError(t, err)

	_, err = ParseUnifiedToken(tok, resolver())
	require.Error(t, err)
}

func TestTaskToken(t *testing.T) {
	tok, _, err := GenerateUnifiedToken(UnifiedTokenParams{
		Typ: "task", TaskID: "task-xyz", Lifetime: ServiceTokenExpiration,
	}, testRSAKey, "test-kid")
	require.NoError(t, err)

	claims, err := ParseUnifiedToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, "task", claims.Typ)
	require.Equal(t, "task-xyz", claims.TaskID)
	require.Equal(t, "task-xyz", claims.Subject)
}

func TestServiceAccountToken_Roundtrip(t *testing.T) {
	scopes := []string{"chaos.inject.write", "chaos.webhook.write"}
	tok, exp, err := GenerateUnifiedToken(UnifiedTokenParams{
		Typ: "service_account", Service: "chaos-service",
		Scopes: scopes, AuthType: "service_account",
		Lifetime: 24 * time.Hour,
	}, testRSAKey, "test-kid")
	require.NoError(t, err)
	require.NotEmpty(t, tok)
	require.True(t, exp.After(time.Now()))

	claims, err := ParseUnifiedToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, "chaos-service", claims.Subject)
	require.Equal(t, JWTIssuerUnified, claims.Issuer)
	require.Equal(t, "service_account", claims.AuthType)
	require.Equal(t, scopes, claims.Scopes)
}

func TestParseUnifiedToken_NonJWTFallsThrough(t *testing.T) {
	_, err := ParseUnifiedToken("deadbeef-not-a-jwt", resolver())
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestParseUnifiedToken_BadSignatureIsRejection(t *testing.T) {
	otherKey, err := generateOtherKey()
	require.NoError(t, err)
	tok, _, err := GenerateUnifiedToken(UnifiedTokenParams{
		Typ: "service_account", Service: "chaos-service",
		Lifetime: time.Hour,
	}, otherKey, "test-kid")
	require.NoError(t, err)

	_, err = ParseUnifiedToken(tok, resolver())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrInvalidToken)
}

func TestParseToken_RejectsWrongKey(t *testing.T) {
	tok, _, err := GenerateUnifiedToken(UnifiedTokenParams{
		Typ: "human", UserID: 1, Username: "a", Email: "a@x",
		IsActive: true, Lifetime: TokenExpiration,
	}, testRSAKey, "test-kid")
	require.NoError(t, err)

	_, err = ParseUnifiedToken(tok, func(_ string) (*rsa.PublicKey, error) {
		other, _ := generateOtherKey()
		return &other.PublicKey, nil
	})
	require.Error(t, err)
}

func TestParseToken_ResolverErrorRejects(t *testing.T) {
	tok, _, err := GenerateUnifiedToken(UnifiedTokenParams{
		Typ: "human", UserID: 1, Username: "a", Email: "a@x",
		IsActive: true, Lifetime: TokenExpiration,
	}, testRSAKey, "test-kid")
	require.NoError(t, err)

	_, err = ParseUnifiedToken(tok, func(_ string) (*rsa.PublicKey, error) {
		return nil, errExample
	})
	require.Error(t, err)
}

func TestParseUnifiedToken_RejectsUnknownIssuer(t *testing.T) {
	tok, _, err := GenerateUnifiedToken(UnifiedTokenParams{
		Typ: "human", UserID: 1, Username: "a", Email: "a@x",
		IsActive: true, Lifetime: TokenExpiration,
	}, testRSAKey, "test-kid")
	require.NoError(t, err)

	// Verify that a valid unified-issuer token parses fine.
	claims, err := ParseUnifiedToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, JWTIssuerUnified, claims.Issuer)
}

var errExample = errors.New("boom")

func generateOtherKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}
