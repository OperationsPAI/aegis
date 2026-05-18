package cluster

import (
	"context"
	"errors"
	"strings"
	"testing"

	clichecks "aegis/cli/cluster"
)

type fakeRunner struct {
	results []clichecks.Result
	err     error
}

func (f fakeRunner) Run(_ context.Context) ([]clichecks.Result, error) {
	return f.results, f.err
}

func TestGetClusterStatusMapsKnownCheckIDsToPortalIDs(t *testing.T) {
	svc := NewService(fakeRunner{
		results: []clichecks.Result{
			{ID: "k8s.exp-namespace", Status: clichecks.StatusOK, Detail: "namespace exp present"},
			{ID: "db.redis", Status: clichecks.StatusOK, Detail: "dialed redis:6379"},
			{ID: "db.mysql", Status: clichecks.StatusFail, Detail: "cannot dial 127.0.0.1:3306", Fix: "start mysql"},
			{ID: "db.etcd", Status: clichecks.StatusOK, Detail: "dialed etcd"},
			{ID: "db.clickhouse", Status: clichecks.StatusWarn, Detail: "slow handshake"},
			{ID: "otel.pipeline-to-clickhouse", Status: clichecks.StatusOK, Detail: "otel pipeline healthy"},
			{ID: "registry.parity", Status: clichecks.StatusOK, Detail: "registry matches"},
		},
	})

	resp, err := svc.GetClusterStatus(context.Background())
	if err != nil {
		t.Fatalf("GetClusterStatus: %v", err)
	}

	want := map[string]ClusterCheckStatus{
		"chk-k8s":       ClusterCheckOK,
		"chk-redis":     ClusterCheckOK,
		"chk-mysql":     ClusterCheckFail,
		"chk-etcd":      ClusterCheckOK,
		"chk-ch":        ClusterCheckWarn,
		"chk-otel":      ClusterCheckOK,
		"chk-pedestals": ClusterCheckOK,
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

	// db.mysql is failing — its detail must carry the fix hint so the
	// portal's Failing-Checks table can render a remediation string.
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
	svc := NewService(fakeRunner{results: []clichecks.Result{
		{ID: "k8s.exp-namespace", Status: clichecks.StatusOK, Detail: "ok"},
	}})

	resp, err := svc.GetClusterStatus(context.Background())
	if err != nil {
		t.Fatalf("GetClusterStatus: %v", err)
	}
	byID := make(map[string]ClusterCheck, len(resp.Checks))
	for _, c := range resp.Checks {
		byID[c.ID] = c
	}
	for _, id := range []string{"chk-redis", "chk-mysql", "chk-etcd", "chk-ch", "chk-otel", "chk-pedestals"} {
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
	svc := NewService(fakeRunner{results: []clichecks.Result{
		{ID: "k8s.exp-namespace", Status: clichecks.StatusOK, Detail: "ok"},
		{ID: "redis.token-bucket-leaks", Status: clichecks.StatusFail, Detail: "3 leaked tokens"},
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
	svc := NewService(fakeRunner{err: want})
	if _, err := svc.GetClusterStatus(context.Background()); !errors.Is(err, want) {
		t.Errorf("expected runner error to propagate, got %v", err)
	}
}

