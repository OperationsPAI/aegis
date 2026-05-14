package consumer

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
)

// fakeIssuer is a minimal in-process tokenIssuer used by the rate-limit
// flow tests. It tracks acquire/release call counts per (taskID, traceID)
// pair, simulates a finite-capacity pool, and lets tests inject errors on
// release.
type fakeIssuer struct {
	mu             sync.Mutex
	name           string
	capacity       int
	inUse          int
	acquireCalls   int
	releaseCalls   int
	acquireErr     error
	releaseErr     error
	waitCalls      int
	exhausted      bool // when true, AcquireToken returns false even with capacity
	exhaustedWait  bool // when true, WaitForToken returns false (timeout)
}

func (f *fakeIssuer) AcquireToken(ctx context.Context, taskID, traceID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquireCalls++
	if f.acquireErr != nil {
		return false, f.acquireErr
	}
	if f.exhausted || f.inUse >= f.capacity {
		return false, nil
	}
	f.inUse++
	return true, nil
}

func (f *fakeIssuer) WaitForToken(ctx context.Context, taskID, traceID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waitCalls++
	if f.exhaustedWait {
		return false, nil
	}
	if f.inUse >= f.capacity {
		return false, nil
	}
	f.inUse++
	return true, nil
}

func (f *fakeIssuer) ReleaseToken(ctx context.Context, taskID, traceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseCalls++
	if f.releaseErr != nil {
		return f.releaseErr
	}
	if f.inUse > 0 {
		f.inUse--
	}
	return nil
}

func newFakeIssuer(name string, capacity int) *fakeIssuer {
	return &fakeIssuer{name: name, capacity: capacity}
}

// TestAcquiredTokens_ReleaseRestartOnly simulates the failure path where
// helm-apply errored: the restart token was acquired but the warming token
// never was. The deferred release must release the restart token exactly
// once and must NOT touch the warming pool.
func TestAcquiredTokens_ReleaseRestartOnly(t *testing.T) {
	restart := newFakeIssuer("restart", 5)
	warming := newFakeIssuer("warming", 30)

	// Simulate acquire of restart only (helm-apply failed before swap).
	if ok, err := restart.AcquireToken(context.Background(), "t1", "tr1"); err != nil || !ok {
		t.Fatalf("restart acquire: ok=%v err=%v", ok, err)
	}

	tokens := acquiredTokens{restart: true, warming: false}
	tokens.release(context.Background(), restart, warming, "t1", "tr1", logrus.NewEntry(logrus.StandardLogger()))

	if restart.releaseCalls != 1 {
		t.Fatalf("restart release calls = %d, want 1", restart.releaseCalls)
	}
	if warming.releaseCalls != 0 {
		t.Fatalf("warming release calls = %d, want 0 (never acquired)", warming.releaseCalls)
	}
	if restart.inUse != 0 {
		t.Fatalf("restart inUse leaked: %d", restart.inUse)
	}
	if tokens.restart || tokens.warming {
		t.Fatalf("tokens flags not cleared after release: %+v", tokens)
	}
}

// TestAcquiredTokens_ReleaseBothCleanly simulates the success path: helm
// apply succeeded → restart token released early → warming token acquired
// → readiness probe succeeded → deferred release fires. By that point
// only the warming token is held; the deferred path must release it and
// must NOT call ReleaseToken on the restart pool a second time.
func TestAcquiredTokens_ReleaseBothCleanly(t *testing.T) {
	restart := newFakeIssuer("restart", 5)
	warming := newFakeIssuer("warming", 30)
	ctx := context.Background()
	logEntry := logrus.NewEntry(logrus.StandardLogger())

	// Acquire restart, then swap (release restart, acquire warming).
	if _, err := restart.AcquireToken(ctx, "t1", "tr1"); err != nil {
		t.Fatalf("restart acquire: %v", err)
	}
	tokens := acquiredTokens{restart: true}

	// Mid-flow swap: release restart, acquire warming.
	if err := restart.ReleaseToken(ctx, "t1", "tr1"); err != nil {
		t.Fatalf("restart release at swap: %v", err)
	}
	tokens.restart = false
	if ok, err := warming.AcquireToken(ctx, "t1", "tr1"); err != nil || !ok {
		t.Fatalf("warming acquire: ok=%v err=%v", ok, err)
	}
	tokens.warming = true

	// Now the deferred release fires.
	tokens.release(ctx, restart, warming, "t1", "tr1", logEntry)

	if restart.releaseCalls != 1 {
		t.Fatalf("restart release calls = %d, want 1 (early-swap only, NOT a second from defer)", restart.releaseCalls)
	}
	if warming.releaseCalls != 1 {
		t.Fatalf("warming release calls = %d, want 1", warming.releaseCalls)
	}
	if restart.inUse != 0 || warming.inUse != 0 {
		t.Fatalf("token leak: restart.inUse=%d warming.inUse=%d", restart.inUse, warming.inUse)
	}
}

// TestAcquiredTokens_HelmFailedNoWarmingAcquired models the exact scenario
// the user called out: when `installPedestal` returns an error, the
// warming token must never be acquired and must never appear in any
// release call. The restart token alone is held and released by defer.
func TestAcquiredTokens_HelmFailedNoWarmingAcquired(t *testing.T) {
	restart := newFakeIssuer("restart", 5)
	warming := newFakeIssuer("warming", 30)
	ctx := context.Background()
	logEntry := logrus.NewEntry(logrus.StandardLogger())

	// Acquire restart token to enter the helm-apply phase.
	if _, err := restart.AcquireToken(ctx, "t1", "tr1"); err != nil {
		t.Fatalf("restart acquire: %v", err)
	}
	tokens := acquiredTokens{restart: true}

	// Simulate helm install failing — control returns to the deferred
	// path without ever acquiring a warming token.
	tokens.release(ctx, restart, warming, "t1", "tr1", logEntry)

	if warming.acquireCalls != 0 {
		t.Fatalf("warming was ACQUIRED on helm-fail path: %d calls", warming.acquireCalls)
	}
	if warming.releaseCalls != 0 {
		t.Fatalf("warming release attempted on helm-fail path: %d calls", warming.releaseCalls)
	}
	if restart.releaseCalls != 1 {
		t.Fatalf("restart release calls = %d, want 1", restart.releaseCalls)
	}
}

// TestAcquiredTokens_NoTokensHeld is a defensive check: the deferred
// release block must be safe to call when neither token is held (e.g.
// rate-limit acquire failed before any token was held).
func TestAcquiredTokens_NoTokensHeld(t *testing.T) {
	restart := newFakeIssuer("restart", 5)
	warming := newFakeIssuer("warming", 30)
	ctx := context.Background()

	tokens := acquiredTokens{}
	tokens.release(ctx, restart, warming, "t1", "tr1", logrus.NewEntry(logrus.StandardLogger()))

	if restart.releaseCalls != 0 || warming.releaseCalls != 0 {
		t.Fatalf("no-tokens release made unexpected calls: restart=%d warming=%d",
			restart.releaseCalls, warming.releaseCalls)
	}
}

// TestAcquiredTokens_ReleaseErrorDoesNotBlockOther verifies that an error
// releasing one token does not prevent the other from being released
// (defensive — both pools should be drained on every defer fire).
func TestAcquiredTokens_ReleaseErrorDoesNotBlockOther(t *testing.T) {
	restart := newFakeIssuer("restart", 5)
	restart.releaseErr = errors.New("redis flake")
	warming := newFakeIssuer("warming", 30)
	ctx := context.Background()
	logEntry := logrus.NewEntry(logrus.StandardLogger())

	if _, err := restart.AcquireToken(ctx, "t1", "tr1"); err != nil {
		t.Fatalf("restart acquire: %v", err)
	}
	if _, err := warming.AcquireToken(ctx, "t1", "tr1"); err != nil {
		t.Fatalf("warming acquire: %v", err)
	}
	tokens := acquiredTokens{restart: true, warming: true}

	tokens.release(ctx, restart, warming, "t1", "tr1", logEntry)

	if restart.releaseCalls != 1 {
		t.Fatalf("restart release calls = %d, want 1 (attempted even though it errors)", restart.releaseCalls)
	}
	if warming.releaseCalls != 1 {
		t.Fatalf("warming release calls = %d, want 1 (must drain even after restart errored)", warming.releaseCalls)
	}
}
