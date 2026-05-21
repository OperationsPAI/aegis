package chaossystem

import (
	"sync"
	"testing"

	chaos "aegis/platform/chaos"
)

// TestChaosOutboundBearerProviderTakesPrecedence pins the boot wiring
// contract: when boot.runtime_stack installs a provider (the SA-minted token
// from consumer.CurrentChaosSAToken), resolveChaosBearer MUST return the
// provider's value and ignore CHAOS_OUTBOUND_BEARER. The previous-attempt
// regression here was forgetting to call SetChaosOutboundBearerProvider at
// all, which silently routed every chaossystem call through the env fallback
// (unset in prod → no Authorization header → 401).
func TestChaosOutboundBearerProviderTakesPrecedence(t *testing.T) {
	prev := chaosBearerProvider
	t.Cleanup(func() {
		chaosBearerProvider = prev
		chaosBearerProviderOnce = sync.Once{}
	})

	SetChaosOutboundBearerProvider(func() string { return "sa-token-from-boot" })
	t.Setenv(chaos.OutboundBearerEnv, "env-token-should-be-ignored")

	if got := resolveChaosBearer(); got != "sa-token-from-boot" {
		t.Fatalf("resolveChaosBearer = %q; want provider token", got)
	}
}

// TestChaosOutboundBearerFallsBackToEnvWhenProviderEmpty ensures the env
// fallback still works during the one-release deprecation window (provider
// installed but mint hasn't completed → returns "").
func TestChaosOutboundBearerFallsBackToEnvWhenProviderEmpty(t *testing.T) {
	prev := chaosBearerProvider
	t.Cleanup(func() {
		chaosBearerProvider = prev
		chaosBearerProviderOnce = sync.Once{}
	})

	SetChaosOutboundBearerProvider(func() string { return "" })
	t.Setenv(chaos.OutboundBearerEnv, "env-fallback")
	chaosBearerProviderOnce = sync.Once{}

	if got := resolveChaosBearer(); got != "env-fallback" {
		t.Fatalf("resolveChaosBearer = %q; want env fallback", got)
	}
}
