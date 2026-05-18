package helm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// recordingGateway captures the arguments InstallPedestal forwards to
// AddRepo / UpdateRepo / Install. Each method can be told to return an
// error so the remote-failure fallback path can be exercised.
type recordingGateway struct {
	addRepoCalls    []addRepoCall
	updateRepoCalls []updateRepoCall
	installCalls    []installCall

	addRepoErr    error
	updateRepoErr error
	// remoteInstallErr / localInstallErr are returned by Install on the
	// 1st / 2nd call respectively, letting the test simulate "remote
	// install fails but local succeeds".
	remoteInstallErr error
	localInstallErr  error
}

type addRepoCall struct{ namespace, name, url string }
type updateRepoCall struct{ namespace, name string }
type installCall struct {
	namespace, releaseName, chartName, version string
	values                                     map[string]any
	overallTimeout, waitTimeout                time.Duration
}

func (g *recordingGateway) AddRepo(namespace, name, url string) error {
	g.addRepoCalls = append(g.addRepoCalls, addRepoCall{namespace, name, url})
	return g.addRepoErr
}

func (g *recordingGateway) UpdateRepo(namespace, name string) error {
	g.updateRepoCalls = append(g.updateRepoCalls, updateRepoCall{namespace, name})
	return g.updateRepoErr
}

func (g *recordingGateway) Install(_ context.Context, namespace, releaseName, chartName, version string, values map[string]any, overallTimeout, waitTimeout time.Duration) error {
	g.installCalls = append(g.installCalls, installCall{namespace, releaseName, chartName, version, values, overallTimeout, waitTimeout})
	if len(g.installCalls) == 1 {
		return g.remoteInstallErr
	}
	return g.localInstallErr
}

// TestInstallPedestal_RemoteSucceedsSkipsLocal verifies the happy path:
// remote AddRepo + UpdateRepo + Install all succeed, so the local fallback
// is never invoked.
func TestInstallPedestal_RemoteSucceedsSkipsLocal(t *testing.T) {
	tmp := t.TempDir()
	localChart := filepath.Join(tmp, "ts-0.3.1.tgz")
	if err := os.WriteFile(localChart, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	gw := &recordingGateway{}

	err := InstallPedestal(context.Background(), gw, PedestalInstallSpec{
		Namespace:      "ts",
		ReleaseName:    "ts",
		ChartName:      "trainticket",
		Version:        "0.3.1",
		RepoURL:        "https://charts.example.com",
		RepoName:       "operations-pai",
		LocalPath:      localChart,
		Values:         map[string]any{"image": "foo"},
		OverallTimeout: 30 * time.Second,
		WaitTimeout:    10 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(gw.installCalls) != 1 {
		t.Fatalf("expected exactly one Install call (remote); got %d", len(gw.installCalls))
	}
	got := gw.installCalls[0]
	if got.chartName != "operations-pai/trainticket" {
		t.Errorf("expected chart=operations-pai/trainticket; got %s", got.chartName)
	}
	if got.overallTimeout != 30*time.Second || got.waitTimeout != 10*time.Second {
		t.Errorf("timeouts not forwarded: overall=%s wait=%s", got.overallTimeout, got.waitTimeout)
	}
}

// TestInstallPedestal_RemoteFailsFallsBackToLocal exercises the fallback
// path that the orchestrator's installPedestal historically relied on —
// after the extraction the shared helper must preserve it byte-for-byte
// so cold-cluster cold-cache deployments keep working.
func TestInstallPedestal_RemoteFailsFallsBackToLocal(t *testing.T) {
	tmp := t.TempDir()
	localChart := filepath.Join(tmp, "ts-0.3.1.tgz")
	if err := os.WriteFile(localChart, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	gw := &recordingGateway{remoteInstallErr: errors.New("remote down")}

	err := InstallPedestal(context.Background(), gw, PedestalInstallSpec{
		Namespace:   "ts",
		ReleaseName: "ts",
		ChartName:   "trainticket",
		Version:     "0.3.1",
		RepoURL:     "https://charts.example.com",
		RepoName:    "operations-pai",
		LocalPath:   localChart,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(gw.installCalls) != 2 {
		t.Fatalf("expected 2 Install calls (remote then local); got %d", len(gw.installCalls))
	}
	if gw.installCalls[1].chartName != localChart {
		t.Errorf("expected fallback chart=%s; got %s", localChart, gw.installCalls[1].chartName)
	}
}

// TestInstallPedestal_OCIBypassesAddRepo verifies that OCI repo URLs skip
// the AddRepo + UpdateRepo pair and pass an oci:// reference to Install.
// Identical to the orchestrator's previous behavior — extraction must
// preserve it.
func TestInstallPedestal_OCIBypassesAddRepo(t *testing.T) {
	gw := &recordingGateway{}
	err := InstallPedestal(context.Background(), gw, PedestalInstallSpec{
		Namespace:   "ts",
		ReleaseName: "ts",
		ChartName:   "trainticket",
		Version:     "0.3.1",
		RepoURL:     "oci://registry.example.com/charts",
		RepoName:    "operations-pai",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(gw.addRepoCalls) != 0 || len(gw.updateRepoCalls) != 0 {
		t.Errorf("OCI install must skip AddRepo/UpdateRepo; got add=%d update=%d", len(gw.addRepoCalls), len(gw.updateRepoCalls))
	}
	if len(gw.installCalls) != 1 {
		t.Fatalf("expected exactly one Install call; got %d", len(gw.installCalls))
	}
	if gw.installCalls[0].chartName != "oci://registry.example.com/charts/trainticket" {
		t.Errorf("OCI ref incorrect; got %s", gw.installCalls[0].chartName)
	}
}

// TestInstallPedestal_NoSourceErrors covers the configuration error path:
// neither remote nor local set means the helper fails fast with a clear
// error before calling Install.
func TestInstallPedestal_NoSourceErrors(t *testing.T) {
	gw := &recordingGateway{}
	err := InstallPedestal(context.Background(), gw, PedestalInstallSpec{
		Namespace:   "ts",
		ReleaseName: "ts",
		ChartName:   "trainticket",
		Version:     "0.3.1",
	})
	if err == nil {
		t.Fatal("expected error for no chart source; got nil")
	}
	if len(gw.installCalls) != 0 {
		t.Errorf("expected no Install calls; got %d", len(gw.installCalls))
	}
}
