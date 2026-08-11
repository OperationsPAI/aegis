package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

var testKey *rsa.PrivateKey

func init() {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	testKey = k
}

func testResolver() crypto.PublicKeyResolver {
	return func(_ string) (*rsa.PublicKey, error) {
		return &testKey.PublicKey, nil
	}
}

func TestMintAndParseInternalToken(t *testing.T) {
	ic := &InternalClaims{
		UserID:       42,
		Username:     "alice",
		Email:        "alice@example.com",
		IsActive:     true,
		IsAdmin:      true,
		Roles:        []string{"admin", "viewer"},
		AuthType:     consts.AuthTypeUser,
		APIKeyID:     7,
		APIKeyScopes: []string{"read", "write"},
	}

	tok, err := MintInternalToken(ic, testKey, "kid-1")
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	parsed, err := ParseInternalToken(tok, testResolver())
	require.NoError(t, err)

	require.Equal(t, 42, parsed.UserID)
	require.Equal(t, "alice", parsed.Username)
	require.Equal(t, "alice@example.com", parsed.Email)
	require.True(t, parsed.IsActive)
	require.True(t, parsed.IsAdmin)
	require.Equal(t, []string{"admin", "viewer"}, parsed.Roles)
	require.Equal(t, consts.AuthTypeUser, parsed.AuthType)
	require.Equal(t, 7, parsed.APIKeyID)
	require.Equal(t, []string{"read", "write"}, parsed.APIKeyScopes)
	require.Equal(t, InternalTokenIssuer, parsed.Issuer)
}

func TestInternalToken_ShortTTL(t *testing.T) {
	ic := &InternalClaims{UserID: 1, IsActive: true}
	tok, err := MintInternalToken(ic, testKey, "kid-1")
	require.NoError(t, err)

	parsed, err := ParseInternalToken(tok, testResolver())
	require.NoError(t, err)

	ttl := parsed.ExpiresAt.Time.Sub(parsed.IssuedAt.Time)
	require.Equal(t, InternalTokenTTL, ttl)
}

func TestInternalToken_WrongIssuerRejected(t *testing.T) {
	// Mint a regular user token (issuer = "aegis"), try to parse it as
	// internal — must be rejected.
	userTok, _, err := crypto.GenerateUnifiedToken(crypto.UnifiedTokenParams{Typ: "human", UserID: 1, Username: "u", Email: "u@x", IsActive: true, Lifetime: time.Hour}, testKey, "kid-1")
	require.NoError(t, err)

	_, err = ParseInternalToken(userTok, testResolver())
	require.ErrorIs(t, err, ErrNotInternalToken)
}

func TestInternalToken_ExpiredRejected(t *testing.T) {
	// Manually craft an expired internal token.
	past := time.Now().Add(-2 * time.Minute)
	claims := &InternalClaims{
		UserID:   1,
		IsActive: true,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    InternalTokenIssuer,
			IssuedAt:  jwt.NewNumericDate(past),
			NotBefore: jwt.NewNumericDate(past),
			ExpiresAt: jwt.NewNumericDate(past.Add(InternalTokenTTL)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "kid-1"
	tok, err := token.SignedString(testKey)
	require.NoError(t, err)

	_, err = ParseInternalToken(tok, testResolver())
	require.Error(t, err)
	require.Contains(t, err.Error(), "token is expired")
}

func TestInternalToken_WrongKeyRejected(t *testing.T) {
	ic := &InternalClaims{UserID: 1, IsActive: true}
	tok, err := MintInternalToken(ic, testKey, "kid-1")
	require.NoError(t, err)

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	otherResolver := func(_ string) (*rsa.PublicKey, error) {
		return &otherKey.PublicKey, nil
	}
	_, err = ParseInternalToken(tok, otherResolver)
	require.Error(t, err)
}

func TestMintInternalTokenFromUnifiedClaims(t *testing.T) {
	src := &crypto.UnifiedClaims{
		UserID:       10,
		Username:     "bob",
		Email:        "bob@x.com",
		IsActive:     true,
		IsAdmin:      false,
		Roles:        []string{"editor"},
		AuthType:     consts.AuthTypeAPIKey,
		APIKeyID:     3,
		APIKeyScopes: []string{"read"},
	}

	tok, err := MintInternalTokenFromUnifiedClaims(src, testKey, "kid-1")
	require.NoError(t, err)

	parsed, err := ParseInternalToken(tok, testResolver())
	require.NoError(t, err)
	require.Equal(t, src.UserID, parsed.UserID)
	require.Equal(t, src.Username, parsed.Username)
	require.Equal(t, src.Email, parsed.Email)
	require.Equal(t, src.Roles, parsed.Roles)
	require.Equal(t, src.AuthType, parsed.AuthType)
	require.Equal(t, src.APIKeyID, parsed.APIKeyID)
	require.Equal(t, src.APIKeyScopes, parsed.APIKeyScopes)
}

func TestSetGinContext_UserPrincipal(t *testing.T) {
	store := make(map[any]any)
	setter := mockSetter{store: store}

	claims := &InternalClaims{
		UserID:       42,
		Username:     "alice",
		Email:        "alice@x.com",
		IsActive:     true,
		IsAdmin:      true,
		Roles:        []string{"admin"},
		AuthType:     consts.AuthTypeUser,
		APIKeyID:     0,
		APIKeyScopes: nil,
	}
	claims.SetGinContext(setter)

	require.Equal(t, 42, store[consts.CtxKeyUserID])
	require.Equal(t, "alice", store[consts.CtxKeyUsername])
	require.Equal(t, "user", store[consts.CtxKeyTokenType])
}

func TestSetGinContext_ServicePrincipal(t *testing.T) {
	store := make(map[any]any)
	setter := mockSetter{store: store}

	claims := &InternalClaims{
		UserID:   0,
		Roles:    []string{consts.ClaimSubjectServicePrefix + "aegis-backend"},
		AuthType: consts.AuthTypeService,
		TaskID:   "task-123",
	}
	claims.SetGinContext(setter)

	require.Equal(t, true, store[consts.CtxKeyIsServiceToken])
	require.Equal(t, "service", store[consts.CtxKeyTokenType])
	require.Equal(t, "task-123", store[consts.CtxKeyTaskID])
}

type mockSetter struct {
	store map[any]any
}

func (m mockSetter) Set(key any, value any) {
	m.store[key] = value
}
