package crypto

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGenerateUnifiedToken_Human(t *testing.T) {
	params := UnifiedTokenParams{
		Typ:      "human",
		UserID:   42,
		Username: "alice",
		Email:    "alice@example.com",
		IsActive: true,
		IsAdmin:  true,
		Roles:    []string{"admin", "user"},
		AuthType: "user",
		Idp:      "local",
		Lifetime: time.Hour,
		Audience: []string{"portal"},
	}

	tok, exp, err := GenerateUnifiedToken(params, testRSAKey, "test-kid")
	require.NoError(t, err)
	require.NotEmpty(t, tok)
	require.True(t, exp.After(time.Now()))

	claims, err := ParseUnifiedToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, "human", claims.Typ)
	require.Equal(t, 42, claims.UserID)
	require.Equal(t, "alice", claims.Username)
	require.Equal(t, "alice@example.com", claims.Email)
	require.True(t, claims.IsActive)
	require.True(t, claims.IsAdmin)
	require.Equal(t, []string{"admin", "user"}, claims.Roles)
	require.Equal(t, "user", claims.AuthType)
	require.Equal(t, "local", claims.Idp)
	require.Equal(t, "aegis", claims.Issuer)
	require.Equal(t, "42", claims.Subject)
}

func TestGenerateUnifiedToken_Service(t *testing.T) {
	params := UnifiedTokenParams{
		Typ:      "service",
		Service:  "aegis-chaos",
		Scopes:   []string{"chaos.inject.write"},
		Lifetime: time.Hour,
	}

	tok, _, err := GenerateUnifiedToken(params, testRSAKey, "test-kid")
	require.NoError(t, err)

	claims, err := ParseUnifiedToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, "service", claims.Typ)
	require.Equal(t, "aegis-chaos", claims.Service)
	require.Equal(t, []string{"chaos.inject.write"}, claims.Scopes)
	require.Empty(t, claims.TaskID)
	require.Equal(t, "aegis-chaos", claims.Subject)
}

func TestGenerateUnifiedToken_Task(t *testing.T) {
	params := UnifiedTokenParams{
		Typ:      "task",
		TaskID:   "task-abc-123",
		Service:  "aegis-worker",
		Lifetime: time.Hour,
	}

	tok, _, err := GenerateUnifiedToken(params, testRSAKey, "test-kid")
	require.NoError(t, err)

	claims, err := ParseUnifiedToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, "task", claims.Typ)
	require.Equal(t, "task-abc-123", claims.TaskID)
	require.Equal(t, "task-abc-123", claims.Subject)
}

func TestGenerateUnifiedToken_ServiceAccount(t *testing.T) {
	params := UnifiedTokenParams{
		Typ:      "service_account",
		Service:  "chaos-service",
		Scopes:   []string{"chaos.inject.write", "chaos.webhook.write"},
		Lifetime: 24 * time.Hour,
	}

	tok, _, err := GenerateUnifiedToken(params, testRSAKey, "test-kid")
	require.NoError(t, err)

	claims, err := ParseUnifiedToken(tok, resolver())
	require.NoError(t, err)
	require.Equal(t, "service_account", claims.Typ)
	require.Equal(t, "chaos-service", claims.Service)
	require.Equal(t, "chaos-service", claims.Subject)
	require.Equal(t, []string{"chaos.inject.write", "chaos.webhook.write"}, claims.Scopes)
}

func TestParseUnifiedToken_WrongIssuer(t *testing.T) {
	// A legacy user token has issuer "rcabench", not "aegis".
	legacyTok, _, err := GenerateToken(1, "u", "u@x", true, false, nil, testRSAKey, "test-kid")
	require.NoError(t, err)

	_, err = ParseUnifiedToken(legacyTok, resolver())
	require.Error(t, err)
}

func TestParseUnifiedToken_InactiveUser(t *testing.T) {
	params := UnifiedTokenParams{
		Typ:      "human",
		UserID:   1,
		Username: "bob",
		IsActive: false,
		Lifetime: time.Hour,
	}

	tok, _, err := GenerateUnifiedToken(params, testRSAKey, "test-kid")
	require.NoError(t, err)

	_, err = ParseUnifiedToken(tok, resolver())
	require.Error(t, err)
	require.Contains(t, err.Error(), "inactive")
}

func TestGenerateUnifiedToken_InvalidType(t *testing.T) {
	params := UnifiedTokenParams{
		Typ:      "bogus",
		Lifetime: time.Hour,
	}
	_, _, err := GenerateUnifiedToken(params, testRSAKey, "test-kid")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown unified token type")
}

func TestGenerateUnifiedToken_ZeroLifetime(t *testing.T) {
	params := UnifiedTokenParams{
		Typ: "human",
	}
	_, _, err := GenerateUnifiedToken(params, testRSAKey, "test-kid")
	require.Error(t, err)
	require.Contains(t, err.Error(), "lifetime")
}
