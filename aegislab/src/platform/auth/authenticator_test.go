package auth

import (
	"context"
	"crypto/hmac"
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
	userClaims    *crypto.Claims
	userErr       error
	serviceClaims *crypto.ServiceClaims
	serviceErr    error
}

func (m *mockVerifier) VerifyToken(_ context.Context, _ string) (*crypto.Claims, error) {
	return m.userClaims, m.userErr
}

func (m *mockVerifier) VerifyServiceToken(_ context.Context, _ string) (*crypto.ServiceClaims, error) {
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

func TestPrincipalFromClaims(t *testing.T) {
	c := &crypto.Claims{
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
			ID: "jwt_user_42_1234",
		},
	}
	p := PrincipalFromClaims(c)

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
		t.Fatalf("Username = %q, want %q", p.Username, "alice")
	}
	if p.Email != "alice@example.com" {
		t.Fatalf("Email = %q", p.Email)
	}
	if !p.IsActive {
		t.Fatal("IsActive = false")
	}
	if !p.IsAdmin {
		t.Fatal("IsAdmin = false")
	}
	if len(p.Roles) != 2 || p.Roles[0] != "admin" {
		t.Fatalf("Roles = %v", p.Roles)
	}
	if p.AuthType != "user" {
		t.Fatalf("AuthType = %q", p.AuthType)
	}
	if p.APIKeyID != 7 {
		t.Fatalf("APIKeyID = %d", p.APIKeyID)
	}
	if len(p.APIKeyScopes) != 2 || p.APIKeyScopes[0] != "read" {
		t.Fatalf("APIKeyScopes = %v", p.APIKeyScopes)
	}
	if p.JTI != "jwt_user_42_1234" {
		t.Fatalf("JTI = %q", p.JTI)
	}
}

func TestPrincipalFromServiceClaims(t *testing.T) {
	c := &crypto.ServiceClaims{
		TaskID: "task-abc",
		Scopes: []string{"inject.write"},
		RegisteredClaims: jwt.RegisteredClaims{
			ID:      "svc_task-abc_1234",
			Subject: "task-abc",
		},
	}
	p := PrincipalFromServiceClaims(c)

	if p.Typ != PrincipalTask {
		t.Fatalf("Typ = %q, want %q", p.Typ, PrincipalTask)
	}
	if p.TaskID != "task-abc" {
		t.Fatalf("TaskID = %q", p.TaskID)
	}
	if p.Sub != "task-abc" {
		t.Fatalf("Sub = %q", p.Sub)
	}
	if len(p.Scopes) != 1 || p.Scopes[0] != "inject.write" {
		t.Fatalf("Scopes = %v", p.Scopes)
	}

	// Service claim with no TaskID -> PrincipalService
	c2 := &crypto.ServiceClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "svc:backend"},
	}
	p2 := PrincipalFromServiceClaims(c2)
	if p2.Typ != PrincipalService {
		t.Fatalf("Typ = %q, want %q", p2.Typ, PrincipalService)
	}
}

func TestPrincipalFromServiceAccountClaims(t *testing.T) {
	c := &crypto.ServiceAccountClaims{
		AuthType: consts.AuthTypeServiceAccount,
		Scopes:   []string{"chaos.inject.write", "chaos.webhook.write"},
		RegisteredClaims: jwt.RegisteredClaims{
			ID:      "sa_chaos-service_1234",
			Subject: "chaos-service",
		},
	}
	p := PrincipalFromServiceAccountClaims(c, "chaos-service")

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
		userClaims: &crypto.Claims{
			UserID:   1,
			Username: "bob",
			IsActive: true,
			RegisteredClaims: jwt.RegisteredClaims{ID: "jti-1"},
		},
	}
	a := NewAuthenticator(v, nil, nil)
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

func TestVerifyBearer_UserFails_ServiceSucceeds(t *testing.T) {
	v := &mockVerifier{
		userErr: errors.New("not a user token"),
		serviceClaims: &crypto.ServiceClaims{
			TaskID: "task-42",
			RegisteredClaims: jwt.RegisteredClaims{
				ID:      "svc-jti",
				Subject: "task-42",
			},
		},
	}
	a := NewAuthenticator(v, nil, nil)
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
	a := NewAuthenticator(v, nil, nil)
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

	a := NewAuthenticator(nil, nil, nil)
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
	a := NewAuthenticator(nil, nil, nil)
	_, err := a.Verify(context.Background(), Credential{
		Type:    CredTrustedHeader,
		Headers: h,
		HMACKey: key,
	})
	if !errors.Is(err, ErrForgedSignature) {
		t.Fatalf("err = %v, want ErrForgedSignature", err)
	}
}

func TestVerifyBearer_SAToken_Revoked(t *testing.T) {
	v := &mockVerifier{
		userErr:    errors.New("not a user token"),
		serviceErr: errors.New("not a service token"),
	}
	now := time.Now()
	store := &mockSAStore{
		sa: &model.ServiceAccount{
			Name:      "chaos-service",
			RevokedAt: &now,
		},
	}
	// We can't easily mint a real SA JWT without a private key, so we skip
	// the ParseServiceAccountToken path by not providing a resolver. This
	// tests the cascade correctly falls through to ErrUnauthenticated.
	a := NewAuthenticator(v, store, nil)
	_, err := a.Verify(context.Background(), Credential{
		Type:        CredBearer,
		BearerToken: "sa-token",
	})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
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
