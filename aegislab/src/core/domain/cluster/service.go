// Package cluster owns the aggregated /api/v2/cluster/status endpoint
// consumed by the portal's ClusterStatus page. The check logic itself
// lives in platform/cluster/preflight (the neutral catalog shared with
// the aegisctl CLI); this domain re-runs that catalog over HTTP and
// adapts each result into the portal-facing ClusterCheck DTO without
// duplicating the probe code.
package cluster

import (
	"context"
	"sync"
	"time"

	preflight "aegis/platform/cluster/preflight"
	"golang.org/x/sync/singleflight"
)

// runStatusChecksTimeout caps a single status-endpoint invocation so a
// stuck probe (slow DNS, hung TCP dial) cannot pin the HTTP goroutine.
const runStatusChecksTimeout = 10 * time.Second

// statusCacheTTL coalesces bursts of polling from the portal so a tab
// refresh / multiple operators viewing the page do not stampede the
// underlying probes. Five seconds is short enough that operators see
// near-realtime status, long enough to absorb typical click rates.
const statusCacheTTL = 5 * time.Second

// CheckRunner runs the preflight catalog against the live cluster
// dependencies and returns one result per check. The default
// implementation drives a process-wide singleton preflight.LiveEnv
// (built once at boot from a config loaded once) so HTTP requests
// reuse pooled connections instead of dialing per request. Tests
// inject a fake.
type CheckRunner interface {
	Run(ctx context.Context) ([]preflight.Result, error)
}

type liveCheckRunner struct {
	env    preflight.CheckEnv
	checks []preflight.Check
}

// NewLiveCheckRunner builds a runner bound to a process-wide CheckEnv.
// The fx wiring in module.go provides one *preflight.LiveEnv per
// process; underlying clients (Redis, MySQL, ClickHouse, etcd, K8s)
// are opened once on first use and reused across every request.
func NewLiveCheckRunner(env preflight.CheckEnv) CheckRunner {
	return liveCheckRunner{env: env, checks: preflight.DefaultChecks()}
}

func (r liveCheckRunner) Run(ctx context.Context) ([]preflight.Result, error) {
	out := make([]preflight.Result, 0, len(r.checks))
	for _, c := range r.checks {
		res := c.Run(ctx, r.env)
		if res.ID == "" {
			res.ID = c.ID
		}
		out = append(out, res)
	}
	return out, nil
}

// NewLiveEnvFromDisk loads config once and constructs a singleton
// preflight.LiveEnv consumed by NewLiveCheckRunner. Invoked exactly
// once at fx boot.
func NewLiveEnvFromDisk() (preflight.CheckEnv, error) {
	cfg, err := preflight.LoadConfig("")
	if err != nil {
		return nil, err
	}
	return preflight.NewLiveEnv(cfg), nil
}

// portalIDMappingEntry pins a stable portal-facing ID + display name to
// an underlying preflight check ID. Checks not in this table still
// surface under their raw IDs (see Service.GetClusterStatus) so the
// Failing-Checks table renders every preflight regression.
type portalIDMappingEntry struct {
	PortalID string
	Name     string
	CheckID  string
}

// portalIDMapping pins stable portal IDs to preflight check IDs.
//
// chk-pedestals is intentionally absent: "Pedestal health" is pod
// liveness, which no current preflight measures (registry.parity is a
// catalog-shape check, not a health check). The portal renders Unknown
// for that card until a real pedestal-health probe lands.
var portalIDMapping = []portalIDMappingEntry{
	{PortalID: "chk-k8s", Name: "K8s API", CheckID: "k8s.exp-namespace"},
	{PortalID: "chk-redis", Name: "Redis", CheckID: "db.redis"},
	{PortalID: "chk-mysql", Name: "MySQL", CheckID: "db.mysql"},
	{PortalID: "chk-etcd", Name: "etcd", CheckID: "db.etcd"},
	{PortalID: "chk-ch", Name: "ClickHouse", CheckID: "db.clickhouse"},
	{PortalID: "chk-otel", Name: "OTel pipeline", CheckID: "otel.pipeline-to-clickhouse"},
}

type cachedResp struct {
	resp      *ClusterStatusResp
	expiresAt time.Time
}

type Service struct {
	runner CheckRunner

	mu     sync.Mutex
	cached cachedResp
	now    func() time.Time

	// sf coalesces concurrent cache-miss callers so a burst of N
	// operators hitting the page within the same TTL window produces
	// exactly one underlying runner.Run, not N. Errors are not cached:
	// singleflight shares the failure with the in-flight cohort and the
	// next call retries naturally.
	sf singleflight.Group
}

// sfKey is the singleflight bucket. The response is global, so a single
// constant key is correct — every concurrent caller wants the same
// result.
const sfKey = "cluster-status"

func NewService(runner CheckRunner) *Service {
	return &Service{runner: runner, now: func() time.Time { return time.Now() }}
}

func (s *Service) GetClusterStatus(ctx context.Context) (*ClusterStatusResp, error) {
	s.mu.Lock()
	if s.cached.resp != nil && s.now().Before(s.cached.expiresAt) {
		resp := s.cached.resp
		s.mu.Unlock()
		return resp, nil
	}
	s.mu.Unlock()

	v, err, _ := s.sf.Do(sfKey, func() (any, error) {
		// Re-check the cache under singleflight: an earlier cohort may
		// have populated it while this caller was waiting on Do.
		s.mu.Lock()
		if s.cached.resp != nil && s.now().Before(s.cached.expiresAt) {
			resp := s.cached.resp
			s.mu.Unlock()
			return resp, nil
		}
		s.mu.Unlock()

		runCtx, cancel := context.WithTimeout(ctx, runStatusChecksTimeout)
		defer cancel()
		results, runErr := s.runner.Run(runCtx)
		if runErr != nil {
			return nil, runErr
		}

		now := s.now()
		resp := s.assembleResponse(results, now)

		s.mu.Lock()
		s.cached = cachedResp{resp: resp, expiresAt: now.Add(statusCacheTTL)}
		s.mu.Unlock()
		return resp, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*ClusterStatusResp), nil
}

func (s *Service) assembleResponse(results []preflight.Result, now time.Time) *ClusterStatusResp {
	resultByID := make(map[string]preflight.Result, len(results))
	for _, r := range results {
		resultByID[r.ID] = r
	}
	mapped := make(map[string]struct{}, len(portalIDMapping))

	checks := make([]ClusterCheck, 0, len(results)+len(portalIDMapping))
	for _, m := range portalIDMapping {
		res, ok := resultByID[m.CheckID]
		if !ok {
			checks = append(checks, ClusterCheck{
				ID:     m.PortalID,
				Name:   m.Name,
				Status: ClusterCheckUnknown,
				Detail: "check not present in this build",
			})
			continue
		}
		mapped[m.CheckID] = struct{}{}
		checks = append(checks, ClusterCheck{
			ID:     m.PortalID,
			Name:   m.Name,
			Status: translateStatus(res.Status),
			Detail: detailWithFix(res),
			// action is intentionally nil: the portal mock seeds
			// restart-pedestal / reseed buttons here, but wiring them
			// up requires the Pedestal install flow (shared
			// helm.Gateway). Deferred until that lands; emitting nil
			// keeps the contract honest.
			Action: nil,
		})
	}

	// Surface any check not in portalIDMapping under its raw ID. The
	// Failing-Checks table on the portal renders the full list, so
	// dropping these would hide real preflight failures.
	for _, r := range results {
		if _, alreadyEmitted := mapped[r.ID]; alreadyEmitted {
			continue
		}
		checks = append(checks, ClusterCheck{
			ID:     r.ID,
			Name:   r.ID,
			Status: translateStatus(r.Status),
			Detail: detailWithFix(r),
		})
	}

	return &ClusterStatusResp{
		Checks: checks,
		// events deferred until source decided
		Events:    []ClusterEvent{},
		UpdatedAt: now.UTC(),
	}
}

func translateStatus(s preflight.Status) ClusterCheckStatus {
	switch s {
	case preflight.StatusOK:
		return ClusterCheckOK
	case preflight.StatusWarn:
		return ClusterCheckWarn
	case preflight.StatusFail:
		return ClusterCheckFail
	case preflight.StatusSkip:
		return ClusterCheckUnknown
	default:
		return ClusterCheckUnknown
	}
}

func detailWithFix(r preflight.Result) string {
	if r.Fix == "" || r.Status == preflight.StatusOK {
		return r.Detail
	}
	return r.Detail + " — fix: " + r.Fix
}
