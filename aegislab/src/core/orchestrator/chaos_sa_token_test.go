package consumer

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"sync"
	"testing"
	"time"

	"aegis/platform/crypto"
	"aegis/platform/jwtkeys"
	"aegis/platform/model"

	"github.com/spf13/viper"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newChaosSATestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.ServiceAccount{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestMintBackendChaosSAToken_RoundTrip(t *testing.T) {
	db := newChaosSATestDB(t)
	if err := db.Create(&model.ServiceAccount{
		Name:   chaosClientSAName,
		Scopes: "chaos.inject.write,chaos.inject.read",
	}).Error; err != nil {
		t.Fatalf("seed sa row: %v", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	signer := &jwtkeys.Signer{PrivateKey: key, Kid: "test-kid"}

	tok, exp, err := mintBackendChaosSAToken(context.Background(), db, signer, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	if time.Until(exp) <= 0 {
		t.Fatalf("expiry not in future: %v", exp)
	}

	resolve := func(string) (*rsa.PublicKey, error) { return &key.PublicKey, nil }
	claims, err := crypto.ParseUnifiedToken(tok, resolve)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Subject != chaosClientSAName {
		t.Errorf("sub = %q; want %q", claims.Subject, chaosClientSAName)
	}
	if claims.Typ != "service_account" {
		t.Errorf("typ = %q; want service_account", claims.Typ)
	}
	var hasInjectWrite bool
	for _, s := range claims.Scopes {
		if s == "chaos.inject.write" {
			hasInjectWrite = true
		}
	}
	if !hasInjectWrite {
		t.Errorf("scopes %v missing chaos.inject.write", claims.Scopes)
	}
}

func TestMintBackendChaosSAToken_MissingRow(t *testing.T) {
	db := newChaosSATestDB(t)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	_, _, err := mintBackendChaosSAToken(context.Background(), db, &jwtkeys.Signer{PrivateKey: key, Kid: "test-kid"}, time.Hour)
	if err == nil {
		t.Fatal("expected error when chaos-client row absent")
	}
	if !strings.Contains(err.Error(), "not seeded") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveChaosOutboundBearer_PrefersSAToken(t *testing.T) {
	prev := chaosSATokenRef.Load()
	t.Cleanup(func() { chaosSATokenRef.Store(prev) })

	t.Setenv(OutboundBearerEnv, "env-fallback-token")
	saTok := "sa-token-xyz"
	chaosSATokenRef.Store(&saTok)
	outboundBearerEnvDeprecationOnce = sync.Once{}

	if got := resolveChaosOutboundBearer(); got != saTok {
		t.Errorf("got %q; want SA token %q", got, saTok)
	}
}

func TestResolveChaosOutboundBearer_FallsBackToEnv(t *testing.T) {
	prev := chaosSATokenRef.Load()
	t.Cleanup(func() { chaosSATokenRef.Store(prev) })
	chaosSATokenRef.Store(nil)

	t.Setenv(OutboundBearerEnv, "env-fallback-token")
	outboundBearerEnvDeprecationOnce = sync.Once{}

	if got := resolveChaosOutboundBearer(); got != "env-fallback-token" {
		t.Errorf("got %q; want env fallback", got)
	}
}

// Dispatcher token-attachment seam: when the SA pointer is set the SDK client
// attaches the SA token; when nil it falls back to CHAOS_OUTBOUND_BEARER.
func TestDefaultChaosServiceClient_AuthorizationPreference(t *testing.T) {
	prev := chaosSATokenRef.Load()
	t.Cleanup(func() { chaosSATokenRef.Store(prev) })

	viper.Set("chaos.service_url", "http://example.invalid")
	t.Cleanup(func() { viper.Set("chaos.service_url", "") })

	t.Run("SA token preferred", func(t *testing.T) {
		t.Setenv(OutboundBearerEnv, "env-fallback")
		saTok := "sa-token-abc"
		chaosSATokenRef.Store(&saTok)
		outboundBearerEnvDeprecationOnce = sync.Once{}

		cli, err := defaultChaosServiceClient()
		if err != nil {
			t.Fatalf("client: %v", err)
		}
		sdkCli, ok := cli.(*sdkChaosServiceClient)
		if !ok {
			t.Fatalf("unexpected client type %T", cli)
		}
		if sdkCli.bearer != saTok {
			t.Errorf("bearer = %q; want SA token %q", sdkCli.bearer, saTok)
		}
	})

	t.Run("env fallback when SA unset", func(t *testing.T) {
		chaosSATokenRef.Store(nil)
		t.Setenv(OutboundBearerEnv, "env-fallback")
		outboundBearerEnvDeprecationOnce = sync.Once{}

		cli, err := defaultChaosServiceClient()
		if err != nil {
			t.Fatalf("client: %v", err)
		}
		sdkCli := cli.(*sdkChaosServiceClient)
		if sdkCli.bearer != "env-fallback" {
			t.Errorf("bearer = %q; want env fallback", sdkCli.bearer)
		}
	})
}
