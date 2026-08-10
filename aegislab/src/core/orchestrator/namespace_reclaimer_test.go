package consumer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"aegis/platform/config"
	helm "aegis/platform/helm"
)

// setViper writes a key into viper and queues a t.Cleanup that resets it.
// The reclaimer reads viper fresh on every tick, so tests cannot rely on
// reset-by-test-suite — each test must own its key lifetime explicitly.
func setViper(t *testing.T, key string, value any) {
	t.Helper()
	prior := viper.Get(key)
	viper.Set(key, value)
	t.Cleanup(func() {
		viper.Set(key, prior)
	})
}

const testTTL = 6 * time.Hour

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return v
}

func TestDecide_SkipsUnmatchedSystem(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	d := Decide(ReclaimCandidate{
		Namespace: "kube-system",
	}, ReclaimConfig{IdleTTL: testTTL, RequireManagedLabel: true}, now)
	require.Equal(t, ReclaimSkip, d.Action)
	require.Contains(t, d.Reason, "no matching system NsPattern")
}

func TestDecide_SkipsMissingManagedLabelWhenRequired(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	c := ReclaimCandidate{
		Namespace:        "sockshop14",
		MatchedSystem:    true,
		SystemName:       "sockshop",
		HelmReleaseFound: true,
		LastDeployed:     now.Add(-30 * time.Hour),
	}
	d := Decide(c, ReclaimConfig{IdleTTL: testTTL, RequireManagedLabel: true}, now)
	require.Equal(t, ReclaimSkip, d.Action)
	require.Contains(t, d.Reason, "missing app.kubernetes.io/managed-by=aegis label")
}

func TestDecide_AllowsUnlabeledWhenRequireIsOff(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	c := ReclaimCandidate{
		Namespace:        "sockshop14",
		MatchedSystem:    true,
		HelmReleaseFound: true,
		LastDeployed:     now.Add(-30 * time.Hour),
	}
	d := Decide(c, ReclaimConfig{IdleTTL: testTTL, RequireManagedLabel: false}, now)
	require.Equal(t, ReclaimReclaim, d.Action)
}

func TestDecide_SkipsActiveLock(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	c := ReclaimCandidate{
		Namespace:         "hs0",
		MatchedSystem:     true,
		HasManagedByLabel: true,
		HelmReleaseFound:  true,
		LastDeployed:      now.Add(-30 * time.Hour),
		NsLocked:          true,
	}
	d := Decide(c, ReclaimConfig{IdleTTL: testTTL, RequireManagedLabel: true}, now)
	require.Equal(t, ReclaimSkip, d.Action)
	require.Contains(t, d.Reason, "active trace lock")
}

func TestDecide_SkipsActiveChaos(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	c := ReclaimCandidate{
		Namespace:         "hs0",
		MatchedSystem:     true,
		HasManagedByLabel: true,
		HelmReleaseFound:  true,
		LastDeployed:      now.Add(-30 * time.Hour),
		ActiveChaosCount:  3,
	}
	d := Decide(c, ReclaimConfig{IdleTTL: testTTL, RequireManagedLabel: true}, now)
	require.Equal(t, ReclaimSkip, d.Action)
	require.Contains(t, d.Reason, "3 active chaos")
}

func TestDecide_SkipsWhenIdleBelowTTL(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	c := ReclaimCandidate{
		Namespace:         "hs0",
		MatchedSystem:     true,
		HasManagedByLabel: true,
		HelmReleaseFound:  true,
		LastDeployed:      now.Add(-1 * time.Hour),
	}
	d := Decide(c, ReclaimConfig{IdleTTL: testTTL, RequireManagedLabel: true}, now)
	require.Equal(t, ReclaimSkip, d.Action)
	require.Contains(t, d.Reason, "idle 1h0m0s < ttl 6h0m0s")
}

func TestDecide_SkipsWhenNoHelmRelease(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	c := ReclaimCandidate{
		Namespace:         "hs0",
		MatchedSystem:     true,
		HasManagedByLabel: true,
		HelmReleaseFound:  false,
	}
	d := Decide(c, ReclaimConfig{IdleTTL: testTTL, RequireManagedLabel: true}, now)
	require.Equal(t, ReclaimSkip, d.Action)
	require.Contains(t, d.Reason, "no helm release")
}

func TestDecide_AllPredicatesTrueReclaim(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	c := ReclaimCandidate{
		Namespace:         "ts3",
		MatchedSystem:     true,
		SystemName:        "ts",
		HasManagedByLabel: true,
		HelmReleaseFound:  true,
		LastDeployed:      now.Add(-72 * time.Hour),
	}
	d := Decide(c, ReclaimConfig{IdleTTL: testTTL, RequireManagedLabel: true}, now)
	require.Equal(t, ReclaimReclaim, d.Action)
	require.Contains(t, d.Reason, "idle 72h0m0s")
}

func TestMatchSystem(t *testing.T) {
	systems := map[string]config.ChaosSystemConfig{
		"hs":      {System: "hs", NsPattern: `^hs\d+$`},
		"ts":      {System: "ts", NsPattern: `^ts\d+$`},
		"sockshop": {System: "sockshop", NsPattern: `^sockshop\d+$`},
	}
	name, ok := MatchSystem("hs7", systems)
	require.True(t, ok)
	require.Equal(t, "hs", name)

	name, ok = MatchSystem("ts0", systems)
	require.True(t, ok)
	require.Equal(t, "ts", name)

	_, ok = MatchSystem("kube-system", systems)
	require.False(t, ok)

	// Beyond-count namespaces (e.g. sockshop14 when Count was rolled back
	// to 10) still match — the reclaimer's pattern check is the source of
	// truth, not config.GetAllNamespaces() which is bounded by Count.
	name, ok = MatchSystem("sockshop42", systems)
	require.True(t, ok)
	require.Equal(t, "sockshop", name)
}

// -------- reconciler-level integration with fakes --------

type fakeHelm struct {
	mu       sync.Mutex
	releases map[string]*helm.ReleaseInfo
	uninstalled []string
	uninstallErr error
}

func newFakeHelm() *fakeHelm { return &fakeHelm{releases: map[string]*helm.ReleaseInfo{}} }

func (f *fakeHelm) GetReleaseInfo(namespace, releaseName string) (*helm.ReleaseInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.releases[namespace+"/"+releaseName]; ok {
		return r, nil
	}
	return nil, nil
}

func (f *fakeHelm) Uninstall(ctx context.Context, namespace, releaseName string, timeout time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.uninstallErr != nil {
		return f.uninstallErr
	}
	f.uninstalled = append(f.uninstalled, namespace+"/"+releaseName)
	delete(f.releases, namespace+"/"+releaseName)
	return nil
}

type fakeK8s struct {
	mu        sync.Mutex
	namespaces []string
	labels     map[string]map[string]string
	chaos      map[string]map[string]int
	deleted    []string
	deleteErr  error
	// softReclaimed lists namespaces that received SoftReclaimNamespace calls.
	// The fake assumes every ns has the same "all-non-DB" set of deployments
	// since tests don't need finer granularity than "soft path was taken".
	softReclaimed []string
	softReclaimErr error
}

func (f *fakeK8s) ListClusterNamespaces(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.namespaces))
	copy(out, f.namespaces)
	return out, nil
}

func (f *fakeK8s) GetNamespaceLabels(ctx context.Context, name string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.labels[name], nil
}

func (f *fakeK8s) ListNamespaceChaosResources(ctx context.Context, namespace string) (map[string]int, []error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chaos[namespace], nil
}

func (f *fakeK8s) DeleteNamespace(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, name)
	for i, n := range f.namespaces {
		if n == name {
			f.namespaces = append(f.namespaces[:i], f.namespaces[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeK8s) SoftReclaimNamespace(ctx context.Context, name string) ([]string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.softReclaimErr != nil {
		return nil, nil, f.softReclaimErr
	}
	f.softReclaimed = append(f.softReclaimed, name)
	return []string{"app-frontend", "app-backend"}, []string{"mongodb", "redis"}, nil
}

type fakeLocks struct {
	mu     sync.Mutex
	locked map[string]bool
	err    error
	// callsByNS lets a test assert how many times IsActive was probed per
	// ns (snapshot vs. pre-uninstall recheck).
	callsByNS map[string]int
	// secondCallLocked, when set for a ns, flips IsActive's return value
	// to true on the SECOND call onward for that ns — the race window
	// fixture for the snapshot-then-acquire path.
	secondCallLocked map[string]bool
}

func (f *fakeLocks) IsActive(ctx context.Context, namespace string, now time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	if f.callsByNS == nil {
		f.callsByNS = map[string]int{}
	}
	f.callsByNS[namespace]++
	if f.secondCallLocked[namespace] && f.callsByNS[namespace] >= 2 {
		return true, nil
	}
	return f.locked[namespace], nil
}

type fakeSystems struct {
	all map[string]config.ChaosSystemConfig
}

func (f fakeSystems) GetAll() map[string]config.ChaosSystemConfig { return f.all }

func newTestReclaimer(now time.Time, helmF *fakeHelm, k8sF *fakeK8s, locks *fakeLocks, systems map[string]config.ChaosSystemConfig) *NamespaceReclaimer {
	return &NamespaceReclaimer{
		helm:     helmF,
		helmDrop: helmF,
		k8s:      k8sF,
		locks:    locks,
		systems:  fakeSystems{all: systems},
		now:      func() time.Time { return now },
	}
}

// withReclaimConfig restores any keys this test set on viper. The keys
// reclaim* read live on the global config; tests using the in-process
// reconciler must reset them so they don't bleed into other tests.
func withReclaimConfig(t *testing.T, kv map[string]any) {
	t.Helper()
	for k, v := range kv {
		setViper(t, k, v)
	}
}

func TestReclaimer_TickReclaimsEligibleNamespace(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	helmF := newFakeHelm()
	helmF.releases["hs7/hs7"] = &helm.ReleaseInfo{
		Name: "hs7", Namespace: "hs7", Status: "deployed",
		LastDeployed: now.Add(-30 * time.Hour),
	}
	k8sF := &fakeK8s{
		namespaces: []string{"hs7"},
		labels:     map[string]map[string]string{"hs7": {managedByAegisLabel: managedByAegisValue}},
		chaos:      map[string]map[string]int{},
	}
	systems := map[string]config.ChaosSystemConfig{
		"hs": {System: "hs", NsPattern: `^hs\d+$`},
	}
	withReclaimConfig(t, map[string]any{
		"helm.reclaim.enabled":               true,
		"helm.reclaim.idle_ttl_hours":        6,
		"helm.reclaim.tick_interval_seconds": 600,
		"helm.reclaim.max_deletes_per_tick":  5,
		"helm.reclaim.require_managed_label": true,
	})
	r := newTestReclaimer(now, helmF, k8sF, &fakeLocks{locked: map[string]bool{}}, systems)
	candidates, processed, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, candidates)
	require.Equal(t, 1, processed)
	require.Equal(t, []string{"hs7/hs7"}, helmF.uninstalled)
	require.Equal(t, []string{"hs7"}, k8sF.deleted)
}

func TestReclaimer_TickKillSwitchReturnsNoop(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	helmF := newFakeHelm()
	helmF.releases["hs7/hs7"] = &helm.ReleaseInfo{LastDeployed: now.Add(-30 * time.Hour)}
	k8sF := &fakeK8s{
		namespaces: []string{"hs7"},
		labels:     map[string]map[string]string{"hs7": {managedByAegisLabel: managedByAegisValue}},
	}
	systems := map[string]config.ChaosSystemConfig{"hs": {NsPattern: `^hs\d+$`}}
	withReclaimConfig(t, map[string]any{"helm.reclaim.enabled": false, "helm.reclaim.idle_ttl_hours": 6, "helm.reclaim.max_deletes_per_tick": 5})
	r := newTestReclaimer(now, helmF, k8sF, &fakeLocks{}, systems)

	candidates, processed, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, candidates)
	require.Equal(t, 0, processed)
	require.Empty(t, helmF.uninstalled)
	require.Empty(t, k8sF.deleted)
}

func TestReclaimer_TickBudgetCap(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	helmF := newFakeHelm()
	k8sF := &fakeK8s{
		labels: map[string]map[string]string{},
		chaos:  map[string]map[string]int{},
	}
	// 10 candidates, all stale at progressively older LastDeployed so
	// oldest-first ordering is observable.
	for i := 0; i < 10; i++ {
		ns := fmt.Sprintf("hs%d", i)
		k8sF.namespaces = append(k8sF.namespaces, ns)
		k8sF.labels[ns] = map[string]string{managedByAegisLabel: managedByAegisValue}
		helmF.releases[ns+"/"+ns] = &helm.ReleaseInfo{
			LastDeployed: now.Add(time.Duration(-100+i) * time.Hour),
		}
	}
	systems := map[string]config.ChaosSystemConfig{"hs": {NsPattern: `^hs\d+$`}}
	withReclaimConfig(t, map[string]any{
		"helm.reclaim.enabled":               true,
		"helm.reclaim.idle_ttl_hours":        6,
		"helm.reclaim.max_deletes_per_tick":  5,
		"helm.reclaim.require_managed_label": true,
	})
	r := newTestReclaimer(now, helmF, k8sF, &fakeLocks{}, systems)

	candidates, processed, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 10, candidates)
	require.Equal(t, 5, processed)
	// The oldest five (hs0..hs4 — LastDeployed 100, 99, ..., 96 hours ago)
	// must go first.
	require.Equal(t, []string{"hs0", "hs1", "hs2", "hs3", "hs4"}, k8sF.deleted)
}

func TestReclaimer_TickLabelGateOff(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	helmF := newFakeHelm()
	helmF.releases["sockshop14/sockshop14"] = &helm.ReleaseInfo{
		LastDeployed: now.Add(-30 * time.Hour),
	}
	k8sF := &fakeK8s{
		namespaces: []string{"sockshop14"},
		// no managed-by label
		labels: map[string]map[string]string{"sockshop14": {}},
	}
	systems := map[string]config.ChaosSystemConfig{
		"sockshop": {NsPattern: `^sockshop\d+$`},
	}

	// Label gate ON: skip.
	withReclaimConfig(t, map[string]any{
		"helm.reclaim.enabled":               true,
		"helm.reclaim.idle_ttl_hours":        6,
		"helm.reclaim.max_deletes_per_tick":  5,
		"helm.reclaim.require_managed_label": true,
	})
	r := newTestReclaimer(now, helmF, k8sF, &fakeLocks{}, systems)
	_, processed, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed)

	// Label gate OFF: reclaim.
	withReclaimConfig(t, map[string]any{
		"helm.reclaim.require_managed_label": false,
	})
	_, processed, err = r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, []string{"sockshop14"}, k8sF.deleted)
}

func TestReclaimer_TickRedisErrorTreatsAsLocked(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	helmF := newFakeHelm()
	helmF.releases["hs7/hs7"] = &helm.ReleaseInfo{LastDeployed: now.Add(-30 * time.Hour)}
	k8sF := &fakeK8s{
		namespaces: []string{"hs7"},
		labels:     map[string]map[string]string{"hs7": {managedByAegisLabel: managedByAegisValue}},
	}
	systems := map[string]config.ChaosSystemConfig{"hs": {NsPattern: `^hs\d+$`}}
	withReclaimConfig(t, map[string]any{
		"helm.reclaim.enabled":               true,
		"helm.reclaim.idle_ttl_hours":        6,
		"helm.reclaim.max_deletes_per_tick":  5,
		"helm.reclaim.require_managed_label": true,
	})
	r := newTestReclaimer(now, helmF, k8sF, &fakeLocks{err: errors.New("redis down")}, systems)
	_, processed, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed)
	require.Empty(t, k8sF.deleted)
}

// Allocator submits the ns AFTER snapshot but BEFORE uninstall: the
// pre-uninstall re-probe must see the new lock and skip. Without the
// recheck the reconciler would delete a ns that's now claimed by a live
// inject.
func TestReclaimer_TickLockAcquiredAfterSnapshotSkipsUninstall(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	helmF := newFakeHelm()
	helmF.releases["hs7/hs7"] = &helm.ReleaseInfo{LastDeployed: now.Add(-30 * time.Hour)}
	k8sF := &fakeK8s{
		namespaces: []string{"hs7"},
		labels:     map[string]map[string]string{"hs7": {managedByAegisLabel: managedByAegisValue}},
	}
	systems := map[string]config.ChaosSystemConfig{"hs": {NsPattern: `^hs\d+$`}}
	withReclaimConfig(t, map[string]any{
		"helm.reclaim.enabled":               true,
		"helm.reclaim.idle_ttl_hours":        6,
		"helm.reclaim.max_deletes_per_tick":  5,
		"helm.reclaim.require_managed_label": true,
	})
	locks := &fakeLocks{
		locked:           map[string]bool{},
		secondCallLocked: map[string]bool{"hs7": true},
	}
	r := newTestReclaimer(now, helmF, k8sF, locks, systems)

	candidates, processed, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, candidates, "snapshot saw an eligible candidate")
	require.Equal(t, 0, processed, "recheck before uninstall must veto")
	require.Empty(t, helmF.uninstalled, "no helm uninstall must fire")
	require.Empty(t, k8sF.deleted, "no namespace delete must fire")
	require.GreaterOrEqual(t, locks.callsByNS["hs7"], 2, "expected snapshot + recheck calls")
}

func TestReclaimer_TickActiveChaosBlocks(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	helmF := newFakeHelm()
	helmF.releases["hs7/hs7"] = &helm.ReleaseInfo{LastDeployed: now.Add(-30 * time.Hour)}
	k8sF := &fakeK8s{
		namespaces: []string{"hs7"},
		labels:     map[string]map[string]string{"hs7": {managedByAegisLabel: managedByAegisValue}},
		chaos:      map[string]map[string]int{"hs7": {"networkchaos": 1}},
	}
	systems := map[string]config.ChaosSystemConfig{"hs": {NsPattern: `^hs\d+$`}}
	withReclaimConfig(t, map[string]any{
		"helm.reclaim.enabled":               true,
		"helm.reclaim.idle_ttl_hours":        6,
		"helm.reclaim.max_deletes_per_tick":  5,
		"helm.reclaim.require_managed_label": true,
	})
	r := newTestReclaimer(now, helmF, k8sF, &fakeLocks{}, systems)
	_, processed, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed)
}

func TestReclaimer_TickSoftReclaimSkipsHelmUninstall(t *testing.T) {
	now := mustParse(t, "2026-05-17T12:00:00Z")
	helmF := newFakeHelm()
	helmF.releases["sn0/sn0"] = &helm.ReleaseInfo{
		Name: "sn0", Namespace: "sn0", Status: "deployed",
		LastDeployed: now.Add(-30 * time.Hour),
	}
	k8sF := &fakeK8s{
		namespaces: []string{"sn0"},
		labels:     map[string]map[string]string{"sn0": {managedByAegisLabel: managedByAegisValue}},
		chaos:      map[string]map[string]int{},
	}
	systems := map[string]config.ChaosSystemConfig{
		"sn": {System: "sn", NsPattern: `^sn\d+$`, ReclaimMode: config.ReclaimModeSoft},
	}
	withReclaimConfig(t, map[string]any{
		"helm.reclaim.enabled":               true,
		"helm.reclaim.idle_ttl_hours":        6,
		"helm.reclaim.max_deletes_per_tick":  5,
		"helm.reclaim.require_managed_label": true,
	})
	r := newTestReclaimer(now, helmF, k8sF, &fakeLocks{locked: map[string]bool{}}, systems)
	candidates, processed, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, candidates)
	require.Equal(t, 1, processed)
	// Helm release must NOT be uninstalled; namespace must NOT be deleted.
	require.Empty(t, helmF.uninstalled, "soft reclaim must not call helm uninstall")
	require.Empty(t, k8sF.deleted, "soft reclaim must not delete namespace")
	require.Equal(t, []string{"sn0"}, k8sF.softReclaimed)
}
