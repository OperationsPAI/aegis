package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/model"

	"github.com/golang-jwt/jwt/v5"
)

// --- mocks ---

type mockVerifier struct {
	userClaims    *crypto.UnifiedClaims
	userErr       error
	serviceClaims *crypto.UnifiedClaims
	serviceErr    error
}

func (m *mockVerifier) VerifyToken(_ context.Context, _ string) (*crypto.UnifiedClaims, error) {
	return m.userClaims, m.userErr
}

func (m *mockVerifier) VerifyServiceToken(_ context.Context, _ string) (*crypto.UnifiedClaims, error) {
	return m.serviceClaims, m.serviceErr
}

type mockSAStore struct {
	sa  *model.ServiceAccount
	err error
}

func (m *mockSAStore) FindByName(_ context.Context, _ string) (*model.ServiceAccount, error) {
	return m.sa, m.err
}

// --- converter tests ---

func TestPrincipalFromUnifiedClaims_Human(t *testing.T) {
	c := &crypto.UnifiedClaims{
		Typ:          "human",
		UserID:       42,
		Username:     "alice",
		Email:        "alice@example.com",
		IsActive:     true,
		IsAdmin:      true,
		Roles:        []string{"admin", "user"},
		AuthType:     "user",
		APIKeyID:     7,
		APIKeyScopes: []string{"read", "write"},
		RegisteredClaims: jwt.RegisteredClaims{
			ID:      "jwt_user_42_1234",
			Subject: "42",
		},
	}
	p := PrincipalFromUnifiedClaims(c)

	if p.Typ != PrincipalHuman {
		t.Fatalf("Typ = %q, want %q", p.Typ, PrincipalHuman)
	}
	if p.Sub != "42" {
		t.Fatalf("Sub = %q, want %q", p.Sub, "42")
	}
	if p.UserID != 42 {
		t.Fatalf("UserID = %d, want 42", p.UserID)
	}
	if p.Username != "alice" {
		t.Fatalf("Username = %q", p.Username)
	}
	if !p.IsActive || !p.IsAdmin {
		t.Fatal("admin/active mismatch")
	}
	if len(p.Roles) != 2 {
		t.Fatalf("Roles = %v", p.Roles)
	}
	if p.APIKeyID != 7 || len(p.APIKeyScopes) != 2 {
		t.Fatalf("APIKey mismatch")
	}
	if p.JTI != "jwt_user_42_1234" {
		t.Fatalf("JTI = %q", p.JTI)
	}
}

func TestPrincipalFromUnifiedClaims_Task(t *testing.T) {
	c := &crypto.UnifiedClaims{
		Typ:    "task",
		TaskID: "task-abc",
		Scopes: []string{"inject.write"},
		RegisteredClaims: jwt.RegisteredClaims{
			ID:      "svc_task-abc_1234",
			Subject: "task-abc",
		},
	}
	p := PrincipalFromUnifiedClaims(c)

	if p.Typ != PrincipalTask {
		t.Fatalf("Typ = %q, want %q", p.Typ, PrincipalTask)
	}
	if p.TaskID != "task-abc" {
		t.Fatalf("TaskID = %q", p.TaskID)
	}
	if len(p.Scopes) != 1 {
		t.Fatalf("Scopes = %v", p.Scopes)
	}
}

func TestPrincipalFromUnifiedClaims_ServiceAccount(t *testing.T) {
	c := &crypto.UnifiedClaims{
		Typ:      "service_account",
		AuthType: consts.AuthTypeServiceAccount,
		Username: "chaos-service",
		Scopes:   []string{"chaos.inject.write", "chaos.webhook.write"},
		RegisteredClaims: jwt.RegisteredClaims{
			ID:      "sa_chaos-service_1234",
			Subject: "chaos-service",
		},
	}
	p := PrincipalFromUnifiedClaims(c)

	if p.Typ != PrincipalServiceAccount {
		t.Fatalf("Typ = %q, want %q", p.Typ, PrincipalServiceAccount)
	}
	if p.Sub != "chaos-service" {
		t.Fatalf("Sub = %q", p.Sub)
	}
	if p.Username != "chaos-service" {
		t.Fatalf("Username = %q", p.Username)
	}
	if p.AuthType != consts.AuthTypeServiceAccount {
		t.Fatalf("AuthType = %q", p.AuthType)
	}
	if len(p.Scopes) != 2 {
		t.Fatalf("Scopes = %v", p.Scopes)
	}
}

func TestPrincipalFromTrustedHeaders(t *testing.T) {
	h := TrustedHeaderSet{
		UserID:       "42",
		UserEmail:    "alice@example.com",
		Roles:        "admin,user",
		TokenAud:     "portal",
		TokenJti:     "jti-123",
		Username:     "alice",
		IsActive:     "1",
		IsAdmin:      "0",
		AuthType:     "user",
		APIKeyID:     "7",
		APIKeyScopes: "read,write",
		TaskID:       "",
	}
	p := PrincipalFromTrustedHeaders(h)

	if p.Typ != PrincipalHuman {
		t.Fatalf("Typ = %q, want %q", p.Typ, PrincipalHuman)
	}
	if p.UserID != 42 {
		t.Fatalf("UserID = %d", p.UserID)
	}
	if p.Email != "alice@example.com" {
		t.Fatalf("Email = %q", p.Email)
	}
	if !p.IsActive {
		t.Fatal("IsActive = false")
	}
	if p.IsAdmin {
		t.Fatal("IsAdmin = true, want false")
	}
	if len(p.Roles) != 2 {
		t.Fatalf("Roles = %v", p.Roles)
	}
	if len(p.APIKeyScopes) != 2 {
		t.Fatalf("APIKeyScopes = %v", p.APIKeyScopes)
	}

	// Service principal via trusted headers
	sh := TrustedHeaderSet{
		UserID:   "0",
		Roles:    consts.ClaimSubjectServicePrefix + "backend",
		Username: "service",
		IsActive: "1",
		TaskID:   "task-xyz",
	}
	sp := PrincipalFromTrustedHeaders(sh)
	if sp.Typ != PrincipalTask {
		t.Fatalf("Typ = %q, want %q", sp.Typ, PrincipalTask)
	}
	if sp.TaskID != "task-xyz" {
		t.Fatalf("TaskID = %q", sp.TaskID)
	}
}

// --- Authenticator.Verify tests ---

func TestVerifyBearer_UserTokenSucceeds(t *testing.T) {
	v := &mockVerifier{
		userClaims: &crypto.UnifiedClaims{
			Typ:      "human",
			UserID:   1,
			Username: "bob",
			IsActive: true,
			RegisteredClaims: jwt.RegisteredClaims{ID: "jti-1"},
		},
	}
	a := NewAuthenticator(v, nil, nil, nil)
	p, err := a.Verify(context.Background(), Credential{
		Type:        CredBearer,
		BearerToken: "some-jwt",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Typ != PrincipalHuman {
		t.Fatalf("Typ = %q, want %q", p.Typ, PrincipalHuman)
	}
	if p.Username != "bob" {
		t.Fatalf("Username = %q", p.Username)
	}
}

func TestVerifyBearer_TaskToken(t *testing.T) {
	v := &mockVerifier{
		userClaims: &crypto.UnifiedClaims{
			Typ:    "task",
			TaskID: "task-42",
			RegisteredClaims: jwt.RegisteredClaims{
				ID:      "svc-jti",
				Subject: "task-42",
			},
		},
	}
	a := NewAuthenticator(v, nil, nil, nil)
	p, err := a.Verify(context.Background(), Credential{
		Type:        CredBearer,
		BearerToken: "some-jwt",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Typ != PrincipalTask {
		t.Fatalf("Typ = %q, want %q", p.Typ, PrincipalTask)
	}
	if p.TaskID != "task-42" {
		t.Fatalf("TaskID = %q", p.TaskID)
	}
}

func TestVerifyBearer_BothFail(t *testing.T) {
	v := &mockVerifier{
		userErr:    errors.New("nope"),
		serviceErr: errors.New("nope"),
	}
	a := NewAuthenticator(v, nil, nil, nil)
	_, err := a.Verify(context.Background(), Credential{
		Type:        CredBearer,
		BearerToken: "bad-token",
	})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestVerifyTrustedHeader_ValidHMAC(t *testing.T) {
	key := []byte("test-signing-key")
	h := TrustedHeaderSet{
		UserID:       "42",
		UserEmail:    "alice@example.com",
		Roles:        "admin",
		TokenAud:     "portal",
		TokenJti:     "jti-1",
		Username:     "alice",
		IsActive:     "1",
		IsAdmin:      "1",
		AuthType:     "user",
		APIKeyID:     "0",
		APIKeyScopes: "",
		TaskID:       "",
	}
	h.Signature = computeHMAC(key, h)

	a := NewAuthenticator(nil, nil, nil, nil)
	p, err := a.Verify(context.Background(), Credential{
		Type:    CredTrustedHeader,
		Headers: h,
		HMACKey: key,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Typ != PrincipalHuman {
		t.Fatalf("Typ = %q, want %q", p.Typ, PrincipalHuman)
	}
	if p.UserID != 42 {
		t.Fatalf("UserID = %d", p.UserID)
	}
	if !p.IsAdmin {
		t.Fatal("IsAdmin = false")
	}
}

func TestVerifyTrustedHeader_InvalidHMAC(t *testing.T) {
	key := []byte("test-signing-key")
	h := TrustedHeaderSet{
		UserID:    "42",
		Signature: "deadbeef",
		IsActive:  "1",
	}
	a := NewAuthenticator(nil, nil, nil, nil)
	_, err := a.Verify(context.Background(), Credential{
		Type:    CredTrustedHeader,
		Headers: h,
		HMACKey: key,
	})
	if !errors.Is(err, ErrForgedSignature) {
		t.Fatalf("err = %v, want ErrForgedSignature", err)
	}
}

func TestVerifyBearer_NoResolverSkipsSA(t *testing.T) {
	v := &mockVerifier{
		userErr:    errors.New("not a user token"),
		serviceErr: errors.New("not a service token"),
	}
	// saStore is set but resolve is nil → SA branch is skipped entirely.
	// This verifies the guard `a.saStore != nil && a.resolve != nil`.
	a := NewAuthenticator(v, &mockSAStore{}, nil, nil)
	_, err := a.Verify(context.Background(), Credential{
		Type:        CredBearer,
		BearerToken: "sa-token",
	})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestVerifyBearer_SAToken_Revoked(t *testing.T) {
	v := &mockVerifier{
		userErr:    errors.New("not a user token"),
		serviceErr: errors.New("not a service token"),
	}

	privKey, err := generateTestRSAKey()
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	kid := "test-kid"
	token, _, err := crypto.GenerateUnifiedToken(crypto.UnifiedTokenParams{Typ: "service_account", Service: "chaos-service", Scopes: []string{"chaos.inject.write"}, Lifetime: 1 * time.Hour}, privKey, kid)
	if err != nil {
		t.Fatalf("generate SA token: %v", err)
	}

	now := time.Now()
	store := &mockSAStore{
		sa: &model.ServiceAccount{
			Name:      "chaos-service",
			RevokedAt: &now,
		},
	}
	resolve := func(_ string) (*rsa.PublicKey, error) {
		return &privKey.PublicKey, nil
	}

	a := NewAuthenticator(v, store, resolve, nil)
	_, err = a.Verify(context.Background(), Credential{
		Type:        CredBearer,
		BearerToken: token,
	})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated (revoked)", err)
	}
}

func TestVerifyBearer_SAToken_Valid(t *testing.T) {
	v := &mockVerifier{
		userErr:    errors.New("not a user token"),
		serviceErr: errors.New("not a service token"),
	}

	privKey, err := generateTestRSAKey()
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	token, _, err := crypto.GenerateUnifiedToken(crypto.UnifiedTokenParams{Typ: "service_account", Service: "chaos-service", Scopes: []string{"chaos.inject.write"}, Lifetime: 1 * time.Hour}, privKey, "test-kid")
	if err != nil {
		t.Fatalf("generate SA token: %v", err)
	}

	store := &mockSAStore{
		sa: &model.ServiceAccount{
			Name: "chaos-service",
		},
	}
	resolve := func(_ string) (*rsa.PublicKey, error) {
		return &privKey.PublicKey, nil
	}

	a := NewAuthenticator(v, store, resolve, nil)
	p, err := a.Verify(context.Background(), Credential{
		Type:        CredBearer,
		BearerToken: token,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Typ != PrincipalServiceAccount {
		t.Fatalf("Typ = %q, want %q", p.Typ, PrincipalServiceAccount)
	}
	if p.Sub != "chaos-service" {
		t.Fatalf("Sub = %q, want %q", p.Sub, "chaos-service")
	}
	if len(p.Scopes) != 1 || p.Scopes[0] != "chaos.inject.write" {
		t.Fatalf("Scopes = %v", p.Scopes)
	}
}

func generateTestRSAKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}

func computeHMAC(key []byte, h TrustedHeaderSet) string {
	canonical := strings.Join([]string{
		h.UserID, h.UserEmail, h.Roles, h.TokenAud, h.TokenJti,
		h.Username, h.IsActive, h.IsAdmin, h.AuthType,
		h.APIKeyID, h.APIKeyScopes, h.TaskID,
	}, "|")
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}
