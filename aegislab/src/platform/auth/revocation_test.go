package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"aegis/platform/crypto"

	"github.com/golang-jwt/jwt/v5"
)

type mockRevocationStore struct {
	revoked map[string]bool
	err     error
	lastTTL time.Duration
}

func newMockRevocationStore() *mockRevocationStore {
	return &mockRevocationStore{revoked: make(map[string]bool)}
}

func (m *mockRevocationStore) Revoke(_ context.Context, jti string, ttl time.Duration) error {
	if m.err != nil {
		return m.err
	}
	m.revoked[jti] = true
	m.lastTTL = ttl
	return nil
}

func (m *mockRevocationStore) IsRevoked(_ context.Context, jti string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.revoked[jti], nil
}

func TestRevocationStore_RevokeAndCheck(t *testing.T) {
	store := newMockRevocationStore()
	ctx := context.Background()

	if err := store.Revoke(ctx, "jti-abc", 10*time.Minute); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	revoked, err := store.IsRevoked(ctx, "jti-abc")
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if !revoked {
		t.Fatal("expected jti-abc to be revoked")
	}
}

func TestRevocationStore_NotRevoked(t *testing.T) {
	store := newMockRevocationStore()
	ctx := context.Background()

	revoked, err := store.IsRevoked(ctx, "unknown-jti")
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if revoked {
		t.Fatal("expected unknown-jti to NOT be revoked")
	}
}

func TestAuthenticator_RevokedToken(t *testing.T) {
	store := newMockRevocationStore()
	store.revoked["jti-1"] = true

	v := &mockVerifier{
		userClaims: &crypto.UnifiedClaims{
			UserID:   1,
			Username: "alice",
			IsActive: true,
			RegisteredClaims: jwt.RegisteredClaims{ID: "jti-1"},
		},
	}
	a := NewAuthenticator(v, nil, nil, store)

	_, err := a.Verify(context.Background(), Credential{
		Type:        CredBearer,
		BearerToken: "valid-jwt",
	})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestAuthenticator_RevocationStoreError_FailOpen(t *testing.T) {
	store := newMockRevocationStore()
	store.err = errors.New("redis connection refused")

	v := &mockVerifier{
		userClaims: &crypto.UnifiedClaims{
			UserID:   1,
			Username: "bob",
			IsActive: true,
			RegisteredClaims: jwt.RegisteredClaims{ID: "jti-2"},
		},
	}
	a := NewAuthenticator(v, nil, nil, store)

	p, err := a.Verify(context.Background(), Credential{
		Type:        CredBearer,
		BearerToken: "valid-jwt",
	})
	if err != nil {
		t.Fatalf("fail-open violated: got err %v", err)
	}
	if p.Username != "bob" {
		t.Fatalf("Username = %q, want bob", p.Username)
	}
}

func TestAuthenticator_NoRevocationStore(t *testing.T) {
	v := &mockVerifier{
		userClaims: &crypto.UnifiedClaims{
			UserID:   1,
			Username: "carol",
			IsActive: true,
			RegisteredClaims: jwt.RegisteredClaims{ID: "jti-3"},
		},
	}
	a := NewAuthenticator(v, nil, nil, nil)

	p, err := a.Verify(context.Background(), Credential{
		Type:        CredBearer,
		BearerToken: "valid-jwt",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Username != "carol" {
		t.Fatalf("Username = %q, want carol", p.Username)
	}
}

func TestRevokeToken_TTLFromExpiry(t *testing.T) {
	store := newMockRevocationStore()
	ctx := context.Background()

	future := time.Now().Add(30 * time.Minute)
	if err := RevokeToken(ctx, store, "jti-ttl", future); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	if store.lastTTL < 29*time.Minute || store.lastTTL > 31*time.Minute {
		t.Fatalf("TTL = %v, want ~30m", store.lastTTL)
	}

	// Zero expiry falls back to 24h
	store2 := newMockRevocationStore()
	if err := RevokeToken(ctx, store2, "jti-zero", time.Time{}); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if store2.lastTTL != 24*time.Hour {
		t.Fatalf("TTL = %v, want 24h", store2.lastTTL)
	}

	// Past expiry also falls back to 24h
	store3 := newMockRevocationStore()
	if err := RevokeToken(ctx, store3, "jti-past", time.Now().Add(-1*time.Hour)); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if store3.lastTTL != 24*time.Hour {
		t.Fatalf("TTL = %v, want 24h", store3.lastTTL)
	}
}
