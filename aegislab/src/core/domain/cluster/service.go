// Package cluster owns the aggregated /api/v2/cluster/status endpoint
// consumed by the portal's ClusterStatus page. The check logic itself
// lives in cli/cluster (the aegisctl preflight catalog); this domain
// re-runs that catalog over HTTP and adapts each result into the
// portal-facing ClusterCheck DTO without duplicating the probe code.
package cluster

import (
	"context"
	"time"

	clichecks "aegis/cli/cluster"
)

// CheckRunner runs the preflight catalog against the live cluster
// dependencies and returns one result per check. The default
// implementation builds a fresh cluster.LiveEnv from the same TOML config
// the CLI reads; tests inject a fake.
type CheckRunner interface {
	Run(ctx context.Context) ([]clichecks.Result, error)
}

type liveCheckRunner struct{}

func NewLiveCheckRunner() CheckRunner { return liveCheckRunner{} }

func (liveCheckRunner) Run(ctx context.Context) ([]clichecks.Result, error) {
	cfg, err := clichecks.LoadConfig("")
	if err != nil {
		return nil, err
	}
	env := clichecks.NewLiveEnv(cfg)
	checks := clichecks.DefaultChecks()
	out := make([]clichecks.Result, 0, len(checks))
	for _, c := range checks {
		res := c.Run(ctx, env)
		if res.ID == "" {
			res.ID = c.ID
		}
		out = append(out, res)
	}
	return out, nil
}

// portalIDMapping pins the stable portal-facing IDs / display names to the
// underlying cli/cluster check IDs. Checks not in this map are still
// surfaced (they're useful for the Failing-Checks table) but with the
// raw check ID as both id and name.
var portalIDMapping = []struct {
	portalID string
	name     string
	checkID  string
}{
	{"chk-k8s", "K8s API", "k8s.exp-namespace"},
	{"chk-redis", "Redis", "db.redis"},
	{"chk-mysql", "MySQL", "db.mysql"},
	{"chk-etcd", "etcd", "db.etcd"},
	{"chk-ch", "ClickHouse", "db.clickhouse"},
	{"chk-otel", "OTel pipeline", "otel.pipeline-to-clickhouse"},
	{"chk-pedestals", "Pedestal health", "registry.parity"},
}

type Service struct {
	runner CheckRunner
}

func NewService(runner CheckRunner) *Service { return &Service{runner: runner} }

func (s *Service) GetClusterStatus(ctx context.Context) (*ClusterStatusResp, error) {
	results, err := s.runner.Run(ctx)
	if err != nil {
		return nil, err
	}

	resultByID := make(map[string]clichecks.Result, len(results))
	for _, r := range results {
		resultByID[r.ID] = r
	}
	mapped := make(map[string]struct{}, len(portalIDMapping))

	checks := make([]ClusterCheck, 0, len(results))
	for _, m := range portalIDMapping {
		res, ok := resultByID[m.checkID]
		if !ok {
			checks = append(checks, ClusterCheck{
				ID:     m.portalID,
				Name:   m.name,
				Status: ClusterCheckUnknown,
				Detail: "check not present in this build",
			})
			continue
		}
		mapped[m.checkID] = struct{}{}
		checks = append(checks, ClusterCheck{
			ID:     m.portalID,
			Name:   m.name,
			Status: translateStatus(res.Status),
			Detail: detailWithFix(res),
			// action is intentionally nil: the portal mock seeds restart-
			// pedestal / reseed buttons here, but wiring them up requires
			// the Pedestal install flow (shared helm.Gateway). Deferred
			// until that lands; emitting nil keeps the contract honest.
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
		// TODO: backfill events from an as-yet-undecided source (OTel logs
		// filtered by component? aegislab audit log? a new audit table?).
		// Returning an empty slice today keeps the endpoint shape stable
		// so the portal can stop polling its mock store.
		Events:    []ClusterEvent{},
		UpdatedAt: time.Now().UTC(),
	}, nil
}

func translateStatus(s clichecks.Status) ClusterCheckStatus {
	switch s {
	case clichecks.StatusOK:
		return ClusterCheckOK
	case clichecks.StatusWarn:
		return ClusterCheckWarn
	case clichecks.StatusFail:
		return ClusterCheckFail
	case clichecks.StatusSkip:
		return ClusterCheckUnknown
	default:
		return ClusterCheckUnknown
	}
}

func detailWithFix(r clichecks.Result) string {
	if r.Fix == "" || r.Status == clichecks.StatusOK {
		return r.Detail
	}
	return r.Detail + " — fix: " + r.Fix
}
