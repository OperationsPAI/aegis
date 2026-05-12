package consumer

import (
	"sync"
	"testing"
)

// fakeActivator records every namespace passed to EnsureNamespaceActive so
// tests can assert that monitor.AcquireLock fans out the activation hook
// (issue #194). Keeping it package-local avoids pulling in the real
// *infra/k8s.Controller (which needs a kubeconfig) for unit-level coverage.
type fakeActivator struct {
	mu       sync.Mutex
	calls    []string
	returnEr error
}

func (f *fakeActivator) EnsureNamespaceActive(namespace string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, namespace)
	return f.returnEr
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

// TestMonitor_NamespaceActivator_FakeImplementsInterface is a compile-time
// guard: if NamespaceActivator changes shape, the fake must be updated in
// lockstep so test coverage doesn't silently drop.
func TestMonitor_NamespaceActivator_FakeImplementsInterface(t *testing.T) {
	var _ NamespaceActivator = (*fakeActivator)(nil)
}
