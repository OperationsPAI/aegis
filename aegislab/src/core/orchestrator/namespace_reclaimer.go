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

// Pre-label-convention namespaces on byte-cluster (sockshop0..9, ts0..1) are
// missing this label and need either a manual `kubectl label` or
// `helm.reclaim.require_managed_label=false` to be reclaimable.
const managedByAegisLabel = "app.kubernetes.io/managed-by"
const managedByAegisValue = "aegis"

type ReclaimConfig struct {
	IdleTTL             time.Duration
	RequireManagedLabel bool
}

type ReclaimCandidate struct {
	Namespace         string
	MatchedSystem     bool
	SystemName        string
	HelmReleaseFound  bool
	LastDeployed      time.Time
	HasManagedByLabel bool
	NsLocked          bool
	ActiveChaosCount  int
}

type ReclaimAction string

const (
	ReclaimSkip    ReclaimAction = "skip"
	ReclaimReclaim ReclaimAction = "reclaim"
)

type ReclaimDecision struct {
	Action ReclaimAction
	Reason string
}

// Decide enforces the five predicates in order so the Reason string always
// points at the most restrictive failure first. `now` is a parameter so
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

// MatchSystem refuses to touch namespaces outside a known pool, even with
// --include-unlabeled.
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

type NamespaceReclaimer struct {
	helm     helmInspector
	helmDrop helmReleaseDropper
	k8s      k8sNamespaceManager
	locks    nsLockProbe
	systems  systemConfigLister
	now      func() time.Time
	runOnce  sync.Once
}

func NewNamespaceReclaimer(
	helmGateway *helm.Gateway,
	k8sGateway *k8s.Gateway,
	redisGateway *redis.Gateway,
) *NamespaceReclaimer {
	locks := state.NewLockStore(redisGateway)
	return &NamespaceReclaimer{
		helm:     helmGateway,
		helmDrop: helmGateway,
		k8s:      k8sGateway,
		locks:    locks,
		systems:  systemAccessor{},
		now:      time.Now,
	}
}

type systemAccessor struct{}

func (systemAccessor) GetAll() map[string]config.ChaosSystemConfig {
	return config.GetChaosSystemConfigManager().GetAll()
}

func StartNamespaceReclaimer(ctx context.Context, r *NamespaceReclaimer) {
	if r == nil {
		return
	}
	r.runOnce.Do(func() {
		go r.Run(ctx)
	})
}

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
	return time.Duration(reclaimTickSeconds()) * time.Second
}

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

	uninstallTO := reclaimUninstallTimeout()
	deleteTO := reclaimDeleteTimeout()

	processed := 0
	for i := 0; i < len(eligible) && processed < maxDeletes; i++ {
		if err := ctx.Err(); err != nil {
			return candidates, processed, err
		}
		ns := eligible[i].cand.Namespace
		// Race window between snapshot (start of tick) and the per-candidate
		// uninstall here: an allocator submit could have claimed this ns in
		// the meantime. Re-probe immediately before destruction; sort
		// + per-candidate uninstall latency can easily span tens of seconds
		// with helm's apiserver round-trips.
		locked, lockErr := r.locks.IsActive(ctx, ns, r.now())
		if lockErr != nil {
			logrus.WithField("namespace", ns).WithError(lockErr).Warn("namespace reclaim: lock re-check failed, treating as locked")
			continue
		}
		if locked {
			logrus.WithField("namespace", ns).Info("namespace reclaim: skipping, lock acquired after snapshot")
			continue
		}
		if err := r.reclaimOne(ctx, eligible[i].cand, eligible[i].decision, uninstallTO, deleteTO); err != nil {
			logrus.WithFields(logrus.Fields{
				"namespace": ns,
				"system":    eligible[i].cand.SystemName,
			}).WithError(err).Warn("namespace reclaim: drop failed")
			continue
		}
		processed++
	}
	return candidates, processed, nil
}

// snapshot fills a ReclaimCandidate for one namespace. Per-source fetch
// errors are logged and the field is left zero so Decide treats absent state
// conservatively. A hard error here only happens when the labels lookup
// fails — without that we cannot tell whether the ns is managed by aegis.
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
		// Redis transient errors must NOT unblock a reclaim — assume locked
		// so we err on the side of safety.
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

func (r *NamespaceReclaimer) reclaimOne(ctx context.Context, c ReclaimCandidate, d ReclaimDecision, uninstallTO, deleteTO time.Duration) error {
	logEntry := logrus.WithFields(logrus.Fields{
		"namespace":     c.Namespace,
		"system":        c.SystemName,
		"last_deployed": c.LastDeployed.Format(time.RFC3339),
		"reason":        d.Reason,
	})
	logEntry.Info("namespace reclaim: dropping helm release + namespace")

	uctx, ucancel := context.WithTimeout(ctx, uninstallTO)
	defer ucancel()
	if err := r.helmDrop.Uninstall(uctx, c.Namespace, c.Namespace, uninstallTO); err != nil {
		return fmt.Errorf("helm uninstall %s/%s: %w", c.Namespace, c.Namespace, err)
	}

	dctx, dcancel := context.WithTimeout(ctx, deleteTO)
	defer dcancel()
	if err := r.k8s.DeleteNamespace(dctx, c.Namespace); err != nil {
		return fmt.Errorf("delete namespace %s: %w", c.Namespace, err)
	}
	logEntry.Info("namespace reclaim: drop complete")
	return nil
}

func reclaimEnabled() bool {
	// Default-true semantics: viper.GetBool returns false for unset, so we
	// fall back to viper.IsSet to distinguish "explicitly false" from
	// "not configured".
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

func reclaimUninstallTimeout() time.Duration {
	v := config.GetInt("helm.reclaim.uninstall_timeout_seconds")
	if v <= 0 {
		return 600 * time.Second
	}
	return time.Duration(v) * time.Second
}

func reclaimDeleteTimeout() time.Duration {
	v := config.GetInt("helm.reclaim.delete_timeout_seconds")
	if v <= 0 {
		return 300 * time.Second
	}
	return time.Duration(v) * time.Second
}
