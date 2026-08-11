package consumer

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeActivator records every namespace passed to EnsureNamespaceActive so
// tests can assert that monitor.AcquireLock fans out the activation hook
// (issue #194). Keeping it package-local avoids pulling in the real
// *infra/k8s.Controller (which needs a kubeconfig) for unit-level coverage.
type fakeActivator struct {
	mu       sync.Mutex
	calls    []string
	returnEr error
	// active drives NamespaceIsActive's return for the self-heal path (#531).
	active    bool
	activeErr error
}

func (f *fakeActivator) EnsureNamespaceActive(namespace string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, namespace)
	return f.returnEr
}

func (f *fakeActivator) NamespaceIsActive(_ context.Context, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active, f.activeErr
}

func (f *fakeActivator) callsCopy() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// TestMonitor_SetActivator_RoundTrip pins the contract used by
// RegisterConsumerHandlers: SetActivator stores the activator and
// currentActivator returns it under the same lock the AcquireLock hook reads
// from. Without this, a controller wired at startup could be invisible to
// AcquireLock and the issue #194 reactivation hook would silently no-op.
func TestMonitor_SetActivator_RoundTrip(t *testing.T) {
	m := &monitor{}

	if got := m.currentActivator(); got != nil {
		t.Fatalf("currentActivator on fresh monitor: got %v, want nil", got)
	}

	fa := &fakeActivator{}
	m.SetActivator(fa)

	got := m.currentActivator()
	if got == nil {
		t.Fatal("currentActivator after SetActivator: got nil, want fakeActivator")
	}
	if got != NamespaceActivator(fa) {
		t.Fatalf("currentActivator returned %v, want the wired fakeActivator", got)
	}

	// Replacing should win — guards against double-wiring during test reuse
	// or a future hot-reload that swaps the controller.
	fa2 := &fakeActivator{}
	m.SetActivator(fa2)
	if m.currentActivator() != NamespaceActivator(fa2) {
		t.Fatal("SetActivator did not replace the previous activator")
	}

	// SetActivator(nil) must clear the hook so AcquireLock falls back to its
	// "no activator wired" no-op branch instead of calling into a stale
	// controller that has been torn down.
	m.SetActivator(nil)
	if got := m.currentActivator(); got != nil {
		t.Fatalf("SetActivator(nil) did not clear, got %v", got)
	}
}

// TestMonitor_CfgMuAndWireMu_AreIndependent pins the split-lock contract:
// the cfg-catalog write path (held during RefreshNamespaces) must not block
// the wire-pointer read path (currentContext / currentActivator that
// AcquireLock relies on). Before the split, both ran under the same RWMutex,
// forcing RefreshNamespaces to snapshot m.ctx manually to dodge self-deadlock.
// If a future refactor collapses the two locks back into one, this test
// deadlocks under -race and -timeout, surfacing the regression immediately.
func TestMonitor_CfgMuAndWireMu_AreIndependent(t *testing.T) {
	m := &monitor{ctx: context.Background()}
	m.SetActivator(&fakeActivator{})

	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Both reads route through wireMu; if either still routes through
		// cfgMu, this goroutine deadlocks.
		_ = m.currentContext()
		_ = m.currentActivator()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("currentContext/currentActivator blocked on cfgMu — locks are not independent")
	}
}

// TestMonitor_SetContext_AcquireLock_NoDeadlock hammers SetContext (wireMu
// writer) against currentContext (wireMu reader) and a simulated catalog
// refresh (cfgMu writer) to confirm the split locks stay race-clean under
// -race. Replaces the implicit single-lock invariant the old m.mu encoded.
func TestMonitor_SetContext_AcquireLock_NoDeadlock(t *testing.T) {
	m := &monitor{ctx: context.Background()}

	const goroutines = 8
	const iters = 500

	var stop atomic.Bool
	var wg sync.WaitGroup

	// Writers rewiring the lifecycle ctx.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters && !stop.Load(); j++ {
				ctx, cancel := context.WithCancel(context.Background())
				m.SetContext(ctx)
				cancel()
			}
		}()
	}

	// Readers, modelling AcquireLock's ctx/activator fetch.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters && !stop.Load(); j++ {
				_ = m.currentContext()
				_ = m.currentActivator()
			}
		}()
	}

	// Catalog-side writers, modelling RefreshNamespaces taking cfgMu while
	// the wire-side path keeps moving.
	for i := 0; i < goroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters && !stop.Load(); j++ {
				m.cfgMu.Lock()
				// Read through wireMu while holding cfgMu — this is exactly
				// the pattern RefreshNamespaces now uses (ctx := m.currentContext()).
				_ = m.currentContext()
				m.cfgMu.Unlock()
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		stop.Store(true)
		t.Fatal("split-lock workload deadlocked")
	}
}

// TestMonitor_NamespaceActivator_FakeImplementsInterface is a compile-time
// guard: if NamespaceActivator changes shape, the fake must be updated in
// lockstep so test coverage doesn't silently drop.
func TestMonitor_NamespaceActivator_FakeImplementsInterface(t *testing.T) {
	var _ NamespaceActivator = (*fakeActivator)(nil)
}
