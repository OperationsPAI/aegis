package injection

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"aegis/consts"
	redisinfra "aegis/infra/redis"

	"github.com/spf13/viper"
)

// seedSockshopSystemInViper mirrors module/chaossystem/service_test.go's
// helper. Kept local to avoid a test-only cross-package import that would
// drag the chaossystem test fixtures into this package's compile graph.
func seedSockshopSystemInViper(t *testing.T, name string, count int) func() {
	t.Helper()
	prev := viper.Get("injection.system")
	viper.Set("injection.system."+name, map[string]any{
		"count":           count,
		"ns_pattern":      "^" + name + `\d+$`,
		"extract_pattern": "^(" + name + `)(\d+)$`,
		"display_name":    name,
		"app_label_key":   "app",
		"is_builtin":      true,
		"status":          int(consts.CommonEnabled),
	})
	return func() { viper.Set("injection.system", prev) }
}

// withAllocSeams overrides the allocator's package-level seams (SetNX,
// CompareAndDelete, ns lock probe, ns lock acquire) and restores them on
// test teardown. Only the seams a given test cares about need to be
// non-nil; nil overrides keep the production wiring.
func withAllocSeams(
	t *testing.T,
	setNX func(ctx context.Context, r *redisinfra.Gateway, key, val string, ttl time.Duration) (bool, error),
	compareDel func(ctx context.Context, r *redisinfra.Gateway, key, val string) (int64, error),
	probe func(ctx context.Context, r *redisinfra.Gateway, ns string, now time.Time) (bool, error),
	acquire func(ctx context.Context, r *redisinfra.Gateway, ns string, endTime time.Time, traceID string, now time.Time) error,
) {
	t.Helper()
	origSetNX := allocSetNXFn
	origCompareDel := allocCompareDelFn
	origProbe := nsLockProbeFn
	origAcquire := nsLockAcquireFn
	if setNX != nil {
		allocSetNXFn = setNX
	}
	if compareDel != nil {
		allocCompareDelFn = compareDel
	}
	if probe != nil {
		nsLockProbeFn = probe
	}
	if acquire != nil {
		nsLockAcquireFn = acquire
	}
	t.Cleanup(func() {
		allocSetNXFn = origSetNX
		allocCompareDelFn = origCompareDel
		nsLockProbeFn = origProbe
		nsLockAcquireFn = origAcquire
	})
}

// TestAllocateSurfacesNonLockedAcquireError pins #167 hardening: when
// nsLockAcquire fails for a reason that is NOT ErrNamespaceLocked (e.g. a
// Redis network blip), the allocator must abort and return that error to
// the caller rather than treating it as a contended slot, swallowing the
// error, and eventually returning ErrPoolExhausted.
func TestAllocateSurfacesNonLockedAcquireError(t *testing.T) {
	const system = "allocnetfail"
	cleanup := seedSockshopSystemInViper(t, system, 2)
	defer cleanup()

	netErr := errors.New("dial tcp 10.0.0.1:6379: connect: connection refused")

	withAllocSeams(t,
		func(ctx context.Context, r *redisinfra.Gateway, key, val string, ttl time.Duration) (bool, error) {
			return true, nil
		},
		func(ctx context.Context, r *redisinfra.Gateway, key, val string) (int64, error) {
			return 1, nil
		},
		func(ctx context.Context, r *redisinfra.Gateway, ns string, now time.Time) (bool, error) {
			return false, nil
		},
		func(ctx context.Context, r *redisinfra.Gateway, ns string, endTime time.Time, traceID string, now time.Time) error {
			return netErr
		},
	)

	probe := func(ctx context.Context, ns string) (bool, error) { return true, nil }

	res, err := AllocateNamespaceForRestart(
		context.Background(),
		&redisinfra.Gateway{}, // unused; all redis ops are stubbed
		system,
		time.Now().Add(time.Hour),
		"trace-net",
		probe,
		AllocateOptions{},
	)
	if err == nil {
		t.Fatalf("expected error, got nil result %+v", res)
	}
	if errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("network error must not be masked as ErrPoolExhausted: %v", err)
	}
	if !errors.Is(err, netErr) {
		t.Fatalf("expected wrapped network error, got %v", err)
	}
}

// TestAllocateSkipsLockedSlot pins that the explicit "another trace owns
// this slot" sentinel does fall through to the next index, so the
// allocator's "step over a contended slot" behaviour still works after the
// error-class fidelity fix.
func TestAllocateSkipsLockedSlot(t *testing.T) {
	const system = "allocskip"
	cleanup := seedSockshopSystemInViper(t, system, 3)
	defer cleanup()

	var attempts []string
	withAllocSeams(t,
		func(ctx context.Context, r *redisinfra.Gateway, key, val string, ttl time.Duration) (bool, error) {
			return true, nil
		},
		func(ctx context.Context, r *redisinfra.Gateway, key, val string) (int64, error) {
			return 1, nil
		},
		func(ctx context.Context, r *redisinfra.Gateway, ns string, now time.Time) (bool, error) {
			return false, nil
		},
		func(ctx context.Context, r *redisinfra.Gateway, ns string, endTime time.Time, traceID string, now time.Time) error {
			attempts = append(attempts, ns)
			if ns == fmt.Sprintf("%s0", system) || ns == fmt.Sprintf("%s1", system) {
				return fmt.Errorf("%w: namespace %s held by other trace", ErrNamespaceLocked, ns)
			}
			return nil
		},
	)

	probe := func(ctx context.Context, ns string) (bool, error) { return true, nil }

	res, err := AllocateNamespaceForRestart(
		context.Background(),
		&redisinfra.Gateway{},
		system,
		time.Now().Add(time.Hour),
		"trace-skip",
		probe,
		AllocateOptions{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v (attempts=%v)", err, attempts)
	}
	want := fmt.Sprintf("%s2", system)
	if res.Namespace != want {
		t.Fatalf("namespace = %q, want %q (attempts=%v)", res.Namespace, want, attempts)
	}
	if len(attempts) != 3 {
		t.Fatalf("expected 3 acquire attempts, got %d (%v)", len(attempts), attempts)
	}
}

// TestAllocateReleaseUsesCompareAndDelete pins #167 hardening: the
// deferred release path goes through CompareAndDeleteKey with the calling
// traceID — never through an unconditional DEL. This is what prevents a
// slow allocator (whose lock TTL expired) from blowing away a successor's
// lock when it finally returns.
func TestAllocateReleaseUsesCompareAndDelete(t *testing.T) {
	const (
		system  = "allocreleasecad"
		traceID = "trace-cad"
	)
	cleanup := seedSockshopSystemInViper(t, system, 1)
	defer cleanup()

	var (
		setNXCalls       int
		compareDelCalls  int
		compareDelTrace  string
		compareDelKey    string
		expectedAllocKey = fmt.Sprintf(allocLockKeyPattern, system)
	)

	withAllocSeams(t,
		func(ctx context.Context, r *redisinfra.Gateway, key, val string, ttl time.Duration) (bool, error) {
			setNXCalls++
			if ttl < 30*time.Second {
				t.Errorf("alloc lock TTL %v shorter than 30s — too tight for worst-case allocations", ttl)
			}
			if val != traceID {
				t.Errorf("SetNX value = %q, want traceID %q", val, traceID)
			}
			return true, nil
		},
		func(ctx context.Context, r *redisinfra.Gateway, key, val string) (int64, error) {
			compareDelCalls++
			compareDelKey = key
			compareDelTrace = val
			return 1, nil
		},
		func(ctx context.Context, r *redisinfra.Gateway, ns string, now time.Time) (bool, error) {
			return false, nil
		},
		func(ctx context.Context, r *redisinfra.Gateway, ns string, endTime time.Time, traceID string, now time.Time) error {
			return nil
		},
	)

	probe := func(ctx context.Context, ns string) (bool, error) { return true, nil }

	res, err := AllocateNamespaceForRestart(
		context.Background(),
		&redisinfra.Gateway{},
		system,
		time.Now().Add(time.Hour),
		traceID,
		probe,
		AllocateOptions{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Namespace == "" {
		t.Fatalf("empty namespace on success")
	}
	if setNXCalls != 1 {
		t.Fatalf("SetNX called %d times, want 1", setNXCalls)
	}
	if compareDelCalls != 1 {
		t.Fatalf("CompareAndDeleteKey called %d times, want 1 (release must use CAD, not unconditional DEL)", compareDelCalls)
	}
	if compareDelKey != expectedAllocKey {
		t.Fatalf("CompareAndDeleteKey key = %q, want %q", compareDelKey, expectedAllocKey)
	}
	if compareDelTrace != traceID {
		t.Fatalf("CompareAndDeleteKey value = %q, want traceID %q (release must compare against our own traceID)", compareDelTrace, traceID)
	}
}

// TestAllocLockTTLCoversWorstCase pins the TTL contract — bumped from 10s
// to >=60s after the #167 review. The previous 10s value let real-world
// allocations cross the TTL when etcd writes or k8s API hiccuped.
func TestAllocLockTTLCoversWorstCase(t *testing.T) {
	if allocLockTTL < 30*time.Second {
		t.Fatalf("allocLockTTL = %v; expected >=30s to cover worst-case etcd+k8s+viper latency (#166 hardening)", allocLockTTL)
	}
}
