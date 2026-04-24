package cmd

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/stretchr/testify/require"
)

// fakeGuidedPodLister is a tiny PodLister stub driven by canned counts so we can
// test --install's branching without a real cluster.
type fakeGuidedPodLister struct {
	initialPods int
	// readySequence supplies CountReadyPods results on successive calls to
	// simulate "install → pods appear unready → become ready".
	readySequence []struct{ total, ready int }
	listCalls     int32
	readyCalls    int32
}

func (f *fakeGuidedPodLister) ListPods(_ context.Context, _, _ string) (int, error) {
	atomic.AddInt32(&f.listCalls, 1)
	return f.initialPods, nil
}

func (f *fakeGuidedPodLister) CountReadyPods(_ context.Context, _, _ string) (int, int, error) {
	i := int(atomic.AddInt32(&f.readyCalls, 1)) - 1
	if i >= len(f.readySequence) {
		i = len(f.readySequence) - 1
	}
	s := f.readySequence[i]
	return s.total, s.ready, nil
}

func TestBootstrapGuidedInstallRequiresSystemAndNamespace(t *testing.T) {
	err := bootstrapGuidedInstall(context.Background(), guidedcli.GuidedConfig{System: "ts"})
	require.ErrorContains(t, err, "--system and --namespace")
}

func TestBootstrapGuidedInstallNoopWhenPodsExist(t *testing.T) {
	orig := guidedPodListerHook
	origInstaller := guidedInstallerHook
	defer func() {
		guidedPodListerHook = orig
		guidedInstallerHook = origInstaller
	}()

	guidedPodListerHook = &fakeGuidedPodLister{initialPods: 3}
	installerCalled := false
	guidedInstallerHook = func(_ context.Context, _, _ string) error {
		installerCalled = true
		return nil
	}

	err := bootstrapGuidedInstall(context.Background(), guidedcli.GuidedConfig{
		System: "ts", Namespace: "ts0",
	})
	require.NoError(t, err)
	require.False(t, installerCalled, "installer must not run when namespace already has pods")
}

func TestBootstrapGuidedInstallInstallsWhenEmptyAndWaitsForReady(t *testing.T) {
	orig := guidedPodListerHook
	origInstaller := guidedInstallerHook
	origTimeout := guidedInstallReadyTimeoutSec
	defer func() {
		guidedPodListerHook = orig
		guidedInstallerHook = origInstaller
		guidedInstallReadyTimeoutSec = origTimeout
	}()

	// First CountReadyPods call: unready; second: ready. Loop has a 5s
	// sleep between polls — we only want to exercise the happy path, so
	// return ready on the first probe.
	guidedPodListerHook = &fakeGuidedPodLister{
		initialPods: 0,
		readySequence: []struct{ total, ready int }{
			{total: 2, ready: 2},
		},
	}
	var installerSystem, installerNamespace string
	guidedInstallerHook = func(_ context.Context, system, namespace string) error {
		installerSystem = system
		installerNamespace = namespace
		return nil
	}
	guidedInstallReadyTimeoutSec = 10

	err := bootstrapGuidedInstall(context.Background(), guidedcli.GuidedConfig{
		System: "ts", Namespace: "ts0",
	})
	require.NoError(t, err)
	require.Equal(t, "ts", installerSystem)
	require.Equal(t, "ts0", installerNamespace)
}

func TestBootstrapGuidedInstallPropagatesInstallerError(t *testing.T) {
	orig := guidedPodListerHook
	origInstaller := guidedInstallerHook
	defer func() {
		guidedPodListerHook = orig
		guidedInstallerHook = origInstaller
	}()

	guidedPodListerHook = &fakeGuidedPodLister{initialPods: 0}
	guidedInstallerHook = func(_ context.Context, _, _ string) error {
		return errors.New("helm exploded")
	}

	err := bootstrapGuidedInstall(context.Background(), guidedcli.GuidedConfig{
		System: "ts", Namespace: "ts0",
	})
	require.ErrorContains(t, err, "helm exploded")
	require.ErrorContains(t, err, "chart install failed")
}
