package cluster

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	preflight "aegis/platform/cluster/preflight"
)

type fakeRunner struct {
	results []preflight.Result
	err     error
	calls   int
}

func (f *fakeRunner) Run(_ context.Context) ([]preflight.Result, error) {
	f.calls++
	return f.results, f.err
}

func TestGetClusterStatusMapsKnownCheckIDsToPortalIDs(t *testing.T) {
	svc := NewService(&fakeRunner{
		results: []preflight.Result{
			{ID: "k8s.exp-namespace", Status: preflight.StatusOK, Detail: "namespace exp present"},
			{ID: "db.redis", Status: preflight.StatusOK, Detail: "dialed redis:6379"},
			{ID: "db.mysql", Status: preflight.StatusFail, Detail: "cannot dial 127.0.0.1:3306", Fix: "start mysql"},
			{ID: "db.etcd", Status: preflight.StatusOK, Detail: "dialed etcd"},
			{ID: "db.clickhouse", Status: preflight.StatusWarn, Detail: "slow handshake"},
			{ID: "otel.pipeline-to-clickhouse", Status: preflight.StatusOK, Detail: "otel pipeline healthy"},
		},
	})

	resp, err := svc.GetClusterStatus(context.Background())
	if err != nil {
		t.Fatalf("GetClusterStatus: %v", err)
	}

	want := map[string]ClusterCheckStatus{
		"chk-k8s":   ClusterCheckOK,
		"chk-redis": ClusterCheckOK,
		"chk-mysql": ClusterCheckFail,
		"chk-etcd":  ClusterCheckOK,
		"chk-ch":    ClusterCheckWarn,
		"chk-otel":  ClusterCheckOK,
	}
	got := make(map[string]ClusterCheckStatus, len(resp.Checks))
	for _, c := range resp.Checks {
		got[c.ID] = c.Status
	}
	for id, status := range want {
		if got[id] != status {
			t.Errorf("check %s: status = %q, want %q", id, got[id], status)
		}
	}

	// chk-pedestals was removed from the mapping because no current
	// preflight measures pod liveness. The portal renders Unknown for
	// that card until a real probe lands; the endpoint must not fake
	// it from registry.parity.
	if _, present := got["chk-pedestals"]; present {
		t.Errorf("chk-pedestals must not appear until a real pedestal-health check exists")
	}

	for _, c := range resp.Checks {
		if c.ID == "chk-mysql" && !strings.Contains(c.Detail, "fix: start mysql") {
			t.Errorf("expected mysql detail to include fix hint, got %q", c.Detail)
		}
		if c.Action != nil {
			t.Errorf("expected no actions in v1 response, got %+v on %s", c.Action, c.ID)
		}
	}

	if resp.Events == nil {
		t.Error("expected non-nil empty events slice")
	}
	if len(resp.Events) != 0 {
		t.Errorf("expected 0 events in v1 response, got %d", len(resp.Events))
	}
}

func TestGetClusterStatusEmitsUnknownForMissingMappedCheck(t *testing.T) {
	svc := NewService(&fakeRunner{results: []preflight.Result{
		{ID: "k8s.exp-namespace", Status: preflight.StatusOK, Detail: "ok"},
	}})

	resp, err := svc.GetClusterStatus(context.Background())
	if err != nil {
		t.Fatalf("GetClusterStatus: %v", err)
	}
	byID := make(map[string]ClusterCheck, len(resp.Checks))
	for _, c := range resp.Checks {
		byID[c.ID] = c
	}
	for _, id := range []string{"chk-redis", "chk-mysql", "chk-etcd", "chk-ch", "chk-otel"} {
		c, ok := byID[id]
		if !ok {
			t.Errorf("expected %s to be present even when underlying check is missing", id)
			continue
		}
		if c.Status != ClusterCheckUnknown {
			t.Errorf("%s: expected unknown, got %q", id, c.Status)
		}
	}
}

func TestGetClusterStatusSurfacesUnmappedChecks(t *testing.T) {
	svc := NewService(&fakeRunner{results: []preflight.Result{
		{ID: "k8s.exp-namespace", Status: preflight.StatusOK, Detail: "ok"},
		{ID: "redis.token-bucket-leaks", Status: preflight.StatusFail, Detail: "3 leaked tokens"},
	}})

	resp, err := svc.GetClusterStatus(context.Background())
	if err != nil {
		t.Fatalf("GetClusterStatus: %v", err)
	}
	var found bool
	for _, c := range resp.Checks {
		if c.ID == "redis.token-bucket-leaks" {
			found = true
			if c.Status != ClusterCheckFail {
				t.Errorf("expected fail, got %q", c.Status)
			}
		}
	}
	if !found {
		t.Error("expected unmapped check redis.token-bucket-leaks to be surfaced under its raw ID")
	}
}

func TestGetClusterStatusPropagatesRunnerError(t *testing.T) {
	want := errors.New("boom")
	svc := NewService(&fakeRunner{err: want})
	if _, err := svc.GetClusterStatus(context.Background()); !errors.Is(err, want) {
		t.Errorf("expected runner error to propagate, got %v", err)
	}
}

func TestGetClusterStatusCachesWithinTTL(t *testing.T) {
	runner := &fakeRunner{results: []preflight.Result{
		{ID: "k8s.exp-namespace", Status: preflight.StatusOK, Detail: "ok"},
	}}
	svc := NewService(runner)
	now := time.Unix(1700000000, 0)
	svc.now = func() time.Time { return now }

	if _, err := svc.GetClusterStatus(context.Background()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := svc.GetClusterStatus(context.Background()); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("expected cache hit on second call, runner.calls = %d", runner.calls)
	}

	// After TTL elapses the next call must hit the runner again.
	now = now.Add(statusCacheTTL + time.Millisecond)
	if _, err := svc.GetClusterStatus(context.Background()); err != nil {
		t.Fatalf("third call: %v", err)
	}
	if runner.calls != 2 {
		t.Fatalf("expected cache to expire after TTL, runner.calls = %d", runner.calls)
	}
}

// TestPortalIDMappingReferencesRealCatalogChecks guards against catalog
// drift: if a preflight check is renamed in
// platform/cluster/preflight without updating portalIDMapping the
// portal card silently goes Unknown forever. This test fails loudly
// instead.
func TestPortalIDMappingReferencesRealCatalogChecks(t *testing.T) {
	catalog := make(map[string]struct{})
	for _, c := range preflight.DefaultChecks() {
		catalog[c.ID] = struct{}{}
	}
	for _, m := range portalIDMapping {
		if _, ok := catalog[m.CheckID]; !ok {
			t.Errorf("portalIDMapping references unknown check %q (portal id %q); rename in catalog?",
				m.CheckID, m.PortalID)
		}
	}
}
