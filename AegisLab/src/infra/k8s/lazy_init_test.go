package k8s

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// TestK8sClientNotAvailableErrFormatting verifies the error wrapper used by
// every request-path gateway method returns a stable user-visible prefix and
// preserves the underlying cause via errors.Is/Unwrap. Issue #193.
func TestK8sClientNotAvailableErrFormatting(t *testing.T) {
	cause := errors.New("connection refused")
	got := k8sClientNotAvailableErr(cause)
	if got == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(got.Error(), "kubernetes client not available") {
		t.Errorf("expected canonical prefix, got %q", got.Error())
	}
	if !errors.Is(got, cause) {
		t.Errorf("expected wrapped cause to be retrievable via errors.Is, got %q", got.Error())
	}

	plain := k8sClientNotAvailableErr(nil)
	if plain == nil || plain.Error() != "kubernetes client not available" {
		t.Errorf("expected plain canonical message on nil cause, got %v", plain)
	}
}

// withPoisonedClient temporarily forces every getK8sClient call to return the
// given error so request-path tests can assert non-fatal propagation without
// a live cluster. Restores the original sync.Once + cached state on cleanup.
//
// This is the key regression guard for issue #193: before the refactor a
// poisoned init terminated the process via logrus.Fatalf; after the refactor
// it must surface as a returned error to the caller.
func withPoisonedClient(t *testing.T, cause error) {
	t.Helper()

	prevClient := k8sClient
	prevErr := k8sClientErr

	// Reset the Once to a fresh zero-value, then consume it with a
	// no-op function. After this Do() completes, subsequent
	// getK8sClient() calls will skip the init body and return the
	// cached (k8sClient, k8sClientErr) pair we set below.
	k8sClientOnce = sync.Once{}
	k8sClient = nil
	k8sClientErr = cause
	k8sClientOnce.Do(func() {})

	t.Cleanup(func() {
		// Restore by zeroing the Once again and re-consuming it with
		// the previously-cached pair. We avoid copying sync.Once
		// values directly (which `go vet` flags as unsafe).
		k8sClientOnce = sync.Once{}
		k8sClient = prevClient
		k8sClientErr = prevErr
		k8sClientOnce.Do(func() {})
	})
}

// TestNamespaceHasWorkloadPropagatesClientInitError covers the auto-allocate
// submit hot path (#167). A transient client-construction failure must
// surface as a (false, error) tuple to the caller instead of crashing the
// process. Issue #193.
func TestNamespaceHasWorkloadPropagatesClientInitError(t *testing.T) {
	withPoisonedClient(t, errors.New("simulated apiserver outage"))

	gw := &Gateway{}
	ok, err := gw.NamespaceHasWorkload(context.Background(), "exp")
	if err == nil {
		t.Fatal("expected error when k8s client init fails, got nil")
	}
	if ok {
		t.Errorf("expected ok=false on client init failure, got true")
	}
	if !strings.Contains(err.Error(), "kubernetes client not available") {
		t.Errorf("expected canonical 'kubernetes client not available' prefix, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "simulated apiserver outage") {
		t.Errorf("expected wrapped cause in error, got %q", err.Error())
	}
}

// TestEnsureNamespacePropagatesClientInitError covers the prerequisite
// namespace-creation path on the same hot path. Issue #193.
func TestEnsureNamespacePropagatesClientInitError(t *testing.T) {
	withPoisonedClient(t, errors.New("kubeconfig not found"))

	gw := &Gateway{}
	created, err := gw.EnsureNamespace(context.Background(), "exp")
	if err == nil {
		t.Fatal("expected error when k8s client init fails, got nil")
	}
	if created {
		t.Errorf("expected created=false on client init failure, got true")
	}
	if !strings.Contains(err.Error(), "kubernetes client not available") {
		t.Errorf("expected canonical prefix, got %q", err.Error())
	}
}

// TestCheckHealthPropagatesClientInitError ensures the health probe surfaces
// a non-fatal error rather than terminating the backend. Issue #193.
func TestCheckHealthPropagatesClientInitError(t *testing.T) {
	withPoisonedClient(t, errors.New("apiserver unreachable"))

	gw := &Gateway{}
	err := gw.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error when k8s client init fails, got nil")
	}
	// CheckHealth probes rest-config first, then client, then dynamic
	// client. Any of those failing must surface as a non-fatal error
	// — the assertion is "didn't crash and returned an error".
	if !strings.Contains(err.Error(), "kubernetes client not available") &&
		!strings.Contains(err.Error(), "kubernetes config not available") &&
		!strings.Contains(err.Error(), "dynamic client not available") {
		t.Errorf("expected one of the canonical not-available prefixes, got %q", err.Error())
	}
}
