package consumer

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"aegis/platform/config"
	helm "aegis/platform/helm"
	k8s "aegis/platform/k8s"
	redis "aegis/platform/redis"
	"aegis/core/orchestrator/internal/state"
)

// managedByAegisLabel is the label aegis stamps on every namespace it owns
// (see k8s.Gateway.EnsureNamespace). Pre-label-convention namespaces
// (sockshop0..9, ts0..1 on byte-cluster) are missing this and need either a
// manual `kubectl label` or `helm.reclaim.require_managed_label=false` to be
// reclaimable.
const managedByAegisLabel = "app.kubernetes.io/managed-by"
const managedByAegisValue = "aegis"

// ReclaimConfig is the policy snapshot used by Decide. The reconciler reads
// fresh config at the top of every tick so an etcd flip takes effect on the
// next sweep — there is no in-process cache.
type ReclaimConfig struct {
	IdleTTL             time.Duration
	RequireManagedLabel bool
}

// ReclaimCandidate is the per-namespace state snapshot Decide evaluates. The
// reconciler populates it from helm/k8s/redis; the CLI populates it from
// shell-outs. Keeping Decide a pure function over this struct is what lets
// us unit-test predicates without standing up a live cluster.
type ReclaimCandidate struct {
	Namespace string
	// MatchedSystem is true when Namespace matches at least one registered
	// system's NsPattern. False means "this is not part of any aegis pool";
	// the reclaimer must refuse to touch such namespaces — that's predicate
	// (4) in the design doc.
	MatchedSystem bool
	// SystemName is the matched system identifier (e.g. "sockshop", "hs").
	// Populated only when MatchedSystem is true; used for logging and the
	// --system filter.
	SystemName string
	// HelmReleaseFound reflects whether `helm status <ns> -n <ns>` returned a
	// release at all. False means there's nothing to uninstall — the
	// reclaimer may still want to drop the namespace if the operator
	// pre-uninstalled, but the safer default is to leave a ns with no helm
	// release alone (operator may have set up things manually).
	HelmReleaseFound bool
	// LastDeployed is release.Info.LastDeployed. Zero when HelmReleaseFound
	// is false.
	LastDeployed time.Time
	// HasManagedByLabel is true when the namespace has
	// app.kubernetes.io/managed-by=aegis. Controlled by the predicate (5)
	// kill-switch RequireManagedLabel.
	HasManagedByLabel bool
	// NsLocked is true when an active trace currently holds the namespace
	// lock (Redis monitor:ns:<ns> with future end_time and non-empty
	// trace_id). The reconciler always reads this; the CLI cannot access
	// redis and treats it as false (operator must trust the locking
	// invariant). When this is true the candidate is unconditionally
	// skipped.
	NsLocked bool
	// ActiveChaosCount is the total number of live chaos-mesh.org CRs in
	// the namespace. Non-zero means "fault in progress, do not touch".
	ActiveChaosCount int
}

// ReclaimAction is the verb Decide chose for a candidate.
type ReclaimAction string

const (
	ReclaimSkip    ReclaimAction = "skip"
	ReclaimReclaim ReclaimAction = "reclaim"
)

// ReclaimDecision is the result of one Decide call. Reason is human-readable
// and ends up in both the reconciler log line and the CLI table.
type ReclaimDecision struct {
	Action ReclaimAction
	Reason string
}

// Decide is the pure-function predicate evaluator. It enforces the five
// predicates from the design doc in order so the Reason string always points
// at the most restrictive failure first. now is taken as a parameter so
// tests can pin the clock.
func Decide(c ReclaimCandidate, cfg ReclaimConfig, now time.Time) ReclaimDecision {
	if !c.MatchedSystem {
		return ReclaimDecision{ReclaimSkip, "no matching system NsPattern"}
	}
	if cfg.RequireManagedLabel && !c.HasManagedByLabel {
		return ReclaimDecision{ReclaimSkip, fmt.Sprintf("missing %s=%s label", managedByAegisLabel, managedByAegisValue)}
	}
	if c.NsLocked {
		return ReclaimDecision{ReclaimSkip, "active trace lock"}
	}
	if c.ActiveChaosCount > 0 {
		return ReclaimDecision{ReclaimSkip, fmt.Sprintf("%d active chaos CRs", c.ActiveChaosCount)}
	}
	if !c.HelmReleaseFound {
		return ReclaimDecision{ReclaimSkip, "no helm release found"}
	}
	if c.LastDeployed.IsZero() {
		return ReclaimDecision{ReclaimSkip, "helm release missing LastDeployed timestamp"}
	}
	age := now.Sub(c.LastDeployed)
	if age < cfg.IdleTTL {
		return ReclaimDecision{ReclaimSkip, fmt.Sprintf("idle %s < ttl %s", age.Round(time.Second), cfg.IdleTTL)}
	}
	return ReclaimDecision{ReclaimReclaim, fmt.Sprintf("idle %s >= ttl %s", age.Round(time.Second), cfg.IdleTTL)}
}

// MatchSystem checks <ns> against each registered system's NsPattern and
// returns the first match. Returns ("", false) when no system claims the ns.
// The reclaimer uses this for predicate (4) — we never touch a ns outside a
// known pool, even with --include-unlabeled.
func MatchSystem(ns string, systems map[string]config.ChaosSystemConfig) (string, bool) {
	for name, cfg := range systems {
		pattern := cfg.NsPattern
		if pattern == "" {
			continue
		}
		rx, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		if rx.MatchString(ns) {
			return name, true
		}
	}
	return "", false
}

// reclaimer deps surfaces. Kept as interfaces so tests substitute fakes
// instead of standing up a live cluster / redis.

type helmInspector interface {
	GetReleaseInfo(namespace, releaseName string) (*helm.ReleaseInfo, error)
}

type helmReleaseDropper interface {
	Uninstall(ctx context.Context, namespace, releaseName string, timeout time.Duration) error
}

type k8sNamespaceManager interface {
	ListClusterNamespaces(ctx context.Context) ([]string, error)
	GetNamespaceLabels(ctx context.Context, name string) (map[string]string, error)
	ListNamespaceChaosResources(ctx context.Context, namespace string) (map[string]int, []error)
	DeleteNamespace(ctx context.Context, name string) error
}

type nsLockProbe interface {
	IsActive(ctx context.Context, namespace string, now time.Time) (bool, error)
}

type systemConfigLister interface {
	GetAll() map[string]config.ChaosSystemConfig
}

// NamespaceReclaimer is the background sweep that drops idle benchmark
// namespaces. Sibling of StuckTraceReconciler; same lifecycle wiring.
type NamespaceReclaimer struct {
	helm          helmInspector
	helmDrop      helmReleaseDropper
	k8s           k8sNamespaceManager
	locks         nsLockProbe
	systems       systemConfigLister
	now           func() time.Time
	uninstallTO   time.Duration
	deleteTO      time.Duration
	tickHook      func(candidates, processed int, err error)
	runOnce       sync.Once
}

// NewNamespaceReclaimer wires the production reclaimer. Gateways come in as
// concrete types so the fx wiring stays trivial; the test-only constructor
// (newNamespaceReclaimerForTest) swaps in fakes.
func NewNamespaceReclaimer(
	helmGateway *helm.Gateway,
	k8sGateway *k8s.Gateway,
	redisGateway *redis.Gateway,
) *NamespaceReclaimer {
	locks := state.NewLockStore(redisGateway)
	return &NamespaceReclaimer{
		helm:        helmGateway,
		helmDrop:    helmGateway,
		k8s:         k8sGateway,
		locks:       locks,
		systems:     systemAccessor{},
		now:         time.Now,
		uninstallTO: 5 * time.Minute,
		deleteTO:    5 * time.Minute,
	}
}

// systemAccessor is the production view of the chaos-system config map. It
// reads fresh from viper on every tick so an etcd update takes effect
// immediately — same model as the rest of the reconciler.
type systemAccessor struct{}

func (systemAccessor) GetAll() map[string]config.ChaosSystemConfig {
	return config.GetChaosSystemConfigManager().GetAll()
}

// StartNamespaceReclaimer is the fx hook entry point. Mirrors
// StartStuckTraceReconciler — instance-scoped runOnce so multiple fx
// app cycles (tests + future in-process restart) each get a fresh loop.
func StartNamespaceReclaimer(ctx context.Context, r *NamespaceReclaimer) {
	if r == nil {
		return
	}
	r.runOnce.Do(func() {
		go r.Run(ctx)
	})
}

// Run drives the ticker. Reads tick_interval_seconds at the top of every
// loop so an etcd push retunes cadence without a backend restart. When the
// reclaimer is disabled via dynamic config the loop still ticks (so a
// re-enable takes effect on the next tick) but logs a single "disabled"
// line and returns from tick.
func (r *NamespaceReclaimer) Run(ctx context.Context) {
	if r == nil {
		return
	}
	current := r.resolveInterval()
	logrus.WithFields(logrus.Fields{
		"interval_seconds":     int(current / time.Second),
		"idle_ttl_hours":       reclaimIdleTTLHours(),
		"max_deletes_per_tick": reclaimMaxDeletes(),
		"enabled":              reclaimEnabled(),
		"require_managed":      reclaimRequireManagedLabel(),
	}).Info("NamespaceReclaimer started")
	ticker := time.NewTicker(current)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logrus.Info("NamespaceReclaimer stopping: context cancelled")
			return
		case <-ticker.C:
		}
		candidates, processed, err := r.runTickSafely(ctx)
		fields := logrus.Fields{"candidates": candidates, "processed": processed}
		switch {
		case err != nil:
			logrus.WithFields(fields).WithError(err).Warn("namespace reclaim tick failed")
		case processed > 0:
			logrus.WithFields(fields).Infof("namespace reclaim tick dropped %d namespace(s)", processed)
		case candidates > 0:
			logrus.WithFields(fields).Info("namespace reclaim tick: candidates found but none eligible")
		default:
			logrus.WithFields(fields).Debug("namespace reclaim tick: no candidates")
		}
		if r.tickHook != nil {
			r.tickHook(candidates, processed, err)
		}
		if next := r.resolveInterval(); next != current {
			ticker.Reset(next)
			current = next
		}
	}
}

func (r *NamespaceReclaimer) runTickSafely(ctx context.Context) (candidates, processed int, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			logrus.WithField("panic", rec).Error("NamespaceReclaimer.tick panicked, continuing loop")
			err = fmt.Errorf("tick panic: %v", rec)
		}
	}()
	candidates, processed, err = r.tick(ctx)
	return
}

func (r *NamespaceReclaimer) resolveInterval() time.Duration {
	secs := reclaimTickSeconds()
	return time.Duration(secs) * time.Second
}

// tick reads config, snapshots cluster state for each candidate ns, evaluates
// Decide, and reclaims up to maxDeletesPerTick of the matched candidates
// (oldest LastDeployed first). The return tuple is (candidates_with_reclaim_decision,
// processed_successfully, err).
func (r *NamespaceReclaimer) tick(ctx context.Context) (int, int, error) {
	if !reclaimEnabled() {
		logrus.Debug("namespace reclaimer disabled via helm.reclaim.enabled=false")
		return 0, 0, nil
	}
	cfg := ReclaimConfig{
		IdleTTL:             time.Duration(reclaimIdleTTLHours()) * time.Hour,
		RequireManagedLabel: reclaimRequireManagedLabel(),
	}
	maxDeletes := reclaimMaxDeletes()
	if maxDeletes <= 0 {
		return 0, 0, nil
	}

	systems := r.systems.GetAll()
	if len(systems) == 0 {
		return 0, 0, nil
	}

	nsList, err := r.k8s.ListClusterNamespaces(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("list namespaces: %w", err)
	}
	now := r.now()

	type scored struct {
		cand     ReclaimCandidate
		decision ReclaimDecision
	}
	var eligible []scored
	for _, ns := range nsList {
		sys, matched := MatchSystem(ns, systems)
		if !matched {
			continue
		}
		c, snapErr := r.snapshot(ctx, ns, sys, now)
		if snapErr != nil {
			logrus.WithField("namespace", ns).WithError(snapErr).Warn("namespace reclaim: snapshot failed")
			continue
		}
		d := Decide(c, cfg, now)
		if d.Action == ReclaimReclaim {
			eligible = append(eligible, scored{c, d})
		}
	}

	sort.SliceStable(eligible, func(i, j int) bool {
		return eligible[i].cand.LastDeployed.Before(eligible[j].cand.LastDeployed)
	})

	candidates := len(eligible)
	if candidates == 0 {
		return 0, 0, nil
	}

	processed := 0
	for i := 0; i < len(eligible) && processed < maxDeletes; i++ {
		if err := ctx.Err(); err != nil {
			return candidates, processed, err
		}
		if err := r.reclaimOne(ctx, eligible[i].cand, eligible[i].decision); err != nil {
			logrus.WithFields(logrus.Fields{
				"namespace": eligible[i].cand.Namespace,
				"system":    eligible[i].cand.SystemName,
			}).WithError(err).Warn("namespace reclaim: drop failed")
			continue
		}
		processed++
	}
	return candidates, processed, nil
}

// snapshot fills a ReclaimCandidate for one namespace. Errors during a
// per-source fetch are logged and the field is left zero — the predicate
// evaluator then treats absent state conservatively (e.g. no helm release
// found → skip). A hard error here only happens if we cannot determine
// whether the ns is matched, in which case the caller skips this ns.
func (r *NamespaceReclaimer) snapshot(ctx context.Context, ns, system string, now time.Time) (ReclaimCandidate, error) {
	c := ReclaimCandidate{Namespace: ns, SystemName: system, MatchedSystem: true}

	labels, err := r.k8s.GetNamespaceLabels(ctx, ns)
	if err != nil {
		return c, fmt.Errorf("labels: %w", err)
	}
	if labels[managedByAegisLabel] == managedByAegisValue {
		c.HasManagedByLabel = true
	}

	locked, err := r.locks.IsActive(ctx, ns, now)
	if err != nil {
		// Redis transient errors should not unblock a reclaim — assume
		// locked so we err on the side of safety.
		logrus.WithField("namespace", ns).WithError(err).Warn("namespace reclaim: lock probe failed, treating as locked")
		c.NsLocked = true
		return c, nil
	}
	c.NsLocked = locked

	if !c.NsLocked {
		counts, _ := r.k8s.ListNamespaceChaosResources(ctx, ns)
		for _, n := range counts {
			c.ActiveChaosCount += n
		}
	}

	rel, err := r.helm.GetReleaseInfo(ns, ns)
	if err != nil {
		logrus.WithField("namespace", ns).WithError(err).Warn("namespace reclaim: helm status failed")
		return c, nil
	}
	if rel != nil {
		c.HelmReleaseFound = true
		c.LastDeployed = rel.LastDeployed
	}
	return c, nil
}

func (r *NamespaceReclaimer) reclaimOne(ctx context.Context, c ReclaimCandidate, d ReclaimDecision) error {
	logEntry := logrus.WithFields(logrus.Fields{
		"namespace":     c.Namespace,
		"system":        c.SystemName,
		"last_deployed": c.LastDeployed.Format(time.RFC3339),
		"reason":        d.Reason,
	})
	logEntry.Info("namespace reclaim: dropping helm release + namespace")

	uctx, ucancel := context.WithTimeout(ctx, r.uninstallTO)
	defer ucancel()
	if err := r.helmDrop.Uninstall(uctx, c.Namespace, c.Namespace, r.uninstallTO); err != nil {
		return fmt.Errorf("helm uninstall %s/%s: %w", c.Namespace, c.Namespace, err)
	}

	dctx, dcancel := context.WithTimeout(ctx, r.deleteTO)
	defer dcancel()
	if err := r.k8s.DeleteNamespace(dctx, c.Namespace); err != nil {
		return fmt.Errorf("delete namespace %s: %w", c.Namespace, err)
	}
	logEntry.Info("namespace reclaim: drop complete")
	return nil
}

// Dynamic config getters. Kept as small functions (mirroring
// helmInstallTimeouts in restart_pedestal.go) so the rest of the reclaimer
// stays oblivious to the viper key strings.

func reclaimEnabled() bool {
	// Default-true semantics: when the key is unset, treat as enabled.
	// viper.GetBool returns false for unset, so we fall back to viper.IsSet
	// to distinguish "explicitly set to false" from "not configured".
	if !viper.IsSet("helm.reclaim.enabled") {
		return true
	}
	return config.GetBool("helm.reclaim.enabled")
}

func reclaimIdleTTLHours() int {
	v := config.GetInt("helm.reclaim.idle_ttl_hours")
	if v <= 0 {
		return 6
	}
	return v
}

func reclaimTickSeconds() int {
	v := config.GetInt("helm.reclaim.tick_interval_seconds")
	if v <= 0 {
		return 600
	}
	return v
}

func reclaimMaxDeletes() int {
	v := config.GetInt("helm.reclaim.max_deletes_per_tick")
	if v <= 0 {
		return 5
	}
	return v
}

func reclaimRequireManagedLabel() bool {
	if !viper.IsSet("helm.reclaim.require_managed_label") {
		return true
	}
	return config.GetBool("helm.reclaim.require_managed_label")
}
