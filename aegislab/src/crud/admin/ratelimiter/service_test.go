package ratelimiter

import (
	"testing"

	"aegis/platform/consts"
)

// TestResolveCapacityPrefersLive guards step 1 of the config-storage
// unification: `rate-limiter status` must report the limiter's LIVE MaxTokens
// (what the running limiter is actually enforcing after an operator override),
// not the stale compile-time const ceiling.
func TestResolveCapacityPrefersLive(t *testing.T) {
	live := map[string]int{
		consts.RestartPedestalTokenBucket: 17,
	}

	if got := resolveCapacity(consts.RestartPedestalTokenBucket, live); got != 17 {
		t.Fatalf("expected live capacity 17, got %d (must not fall back to const %d)",
			got, consts.MaxConcurrentRestartPedestal)
	}
}

// TestResolveCapacityFallsBackToConst covers the degraded path: when the
// runtime-worker query channel is down or a bucket is absent from the live
// report, status still renders the const ceiling rather than 0.
func TestResolveCapacityFallsBackToConst(t *testing.T) {
	if got := resolveCapacity(consts.AlgoExecutionTokenBucket, nil); got != consts.MaxConcurrentAlgoExecution {
		t.Fatalf("expected const fallback %d, got %d", consts.MaxConcurrentAlgoExecution, got)
	}

	live := map[string]int{consts.RestartPedestalTokenBucket: 17}
	if got := resolveCapacity(consts.BuildDatapackTokenBucket, live); got != consts.MaxConcurrentBuildDatapack {
		t.Fatalf("expected const fallback %d for unreported bucket, got %d",
			consts.MaxConcurrentBuildDatapack, got)
	}
}

// TestKnownBucketsCoversAllLimiters ensures the canonical bucket set stays in
// sync with the limiters that actually exist — a missing bucket means status
// silently omits a live limiter.
func TestKnownBucketsCoversAllLimiters(t *testing.T) {
	want := []string{
		consts.RestartPedestalTokenBucket,
		consts.NamespaceWarmingTokenBucket,
		consts.BuildContainerTokenBucket,
		consts.AlgoExecutionTokenBucket,
		consts.BuildDatapackTokenBucket,
	}
	got := knownBuckets()
	for _, b := range want {
		if _, ok := got[b]; !ok {
			t.Errorf("knownBuckets missing %q", b)
		}
	}
}
