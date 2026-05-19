package chaos

import (
	"testing"
	"time"
)

// TestCreateInjectionBatch_HappyPath: two healthy children → both running,
// each Apply called exactly once, batch aggregated_status=running.
func TestCreateInjectionBatch_HappyPath(t *testing.T) {
	mgr, exec, db := newTestManager(t)
	_, p1 := seedSystemAndPoint(t, db)

	// Add a second point on a different service so the children differ.
	now := time.Now().UTC()
	svc2 := Service{
		SystemName: "ts", Name: "auth", Instance: "default",
		ChartVersion: "v1.0.0", Status: ServiceActive,
		DiscoveredAt: now, LastSeenAt: now,
	}
	if err := db.Create(&svc2).Error; err != nil {
		t.Fatalf("svc2: %v", err)
	}
	target2 := map[string]any{"namespace": "ts", "app": "auth"}
	p2id, _ := ServiceBoundPointID(PointIdentity{
		System: "ts", Service: "auth", Instance: "default",
		ChartVersion: "v1.0.0", Capability: "pod_kill", Target: target2,
	})
	if err := db.Create(&Point{
		ID: p2id, SystemName: "ts", ServiceID: &svc2.ID,
		CapabilityName: "pod_kill", Target: JSONMap(target2),
		Source: "test", Status: PointActive,
	}).Error; err != nil {
		t.Fatalf("p2: %v", err)
	}

	out, err := mgr.CreateInjectionBatch(t.Context(), CreateBatchInput{
		BatchIdempotencyKey: "batch-1",
		Children: []CreateBatchChild{
			{PointID: p1, IdempotencyKey: "k-c1", Params: map[string]any{"duration_s": 30}},
			{PointID: p2id, IdempotencyKey: "k-c2", Params: map[string]any{"duration_s": 30}},
		},
	})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if got := len(out.Children); got != 2 {
		t.Fatalf("children len: want 2 got %d", got)
	}
	if got := exec.applyCount.Load(); got != 2 {
		t.Fatalf("Apply called %d times; want 2", got)
	}
	for _, c := range out.Children {
		if c.Status != StatusRunning {
			t.Fatalf("child %s status=%s want running", c.ID, c.Status)
		}
		if c.BatchID == nil || *c.BatchID != out.Batch.ID {
			t.Fatalf("child batch_id mismatch: %v", c.BatchID)
		}
	}
	if out.Batch.AggregatedStatus != AggRunning {
		t.Fatalf("aggregated: want running got %q", out.Batch.AggregatedStatus)
	}
}

// TestCreateInjectionBatch_IdempotentReplay: same batch_idempotency_key
// returns the same batch + children with no extra Apply calls.
func TestCreateInjectionBatch_IdempotentReplay(t *testing.T) {
	mgr, exec, db := newTestManager(t)
	_, p1 := seedSystemAndPoint(t, db)

	in := CreateBatchInput{
		BatchIdempotencyKey: "batch-rep",
		Children: []CreateBatchChild{
			{PointID: p1, IdempotencyKey: "k-rep", Params: map[string]any{"duration_s": 30}},
		},
	}
	first, err := mgr.CreateInjectionBatch(t.Context(), in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := mgr.CreateInjectionBatch(t.Context(), in)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.Batch.ID != second.Batch.ID {
		t.Fatalf("batch id mismatch: %q vs %q", first.Batch.ID, second.Batch.ID)
	}
	if got := exec.applyCount.Load(); got != 1 {
		t.Fatalf("Apply called %d times; want 1", got)
	}
}

// TestCreateInjectionBatch_PartialFailure: unknown point on one child must
// fail that child only — sibling proceeds.
func TestCreateInjectionBatch_PartialFailure(t *testing.T) {
	mgr, _, db := newTestManager(t)
	_, p1 := seedSystemAndPoint(t, db)

	out, err := mgr.CreateInjectionBatch(t.Context(), CreateBatchInput{
		BatchIdempotencyKey: "batch-partial",
		Children: []CreateBatchChild{
			{PointID: p1, IdempotencyKey: "k-ok"},
			{PointID: "doesnotexist", IdempotencyKey: "k-bad"},
		},
	})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if got := len(out.Children); got != 2 {
		t.Fatalf("children len: want 2 got %d", got)
	}
	var ok, bad *Injection
	for i := range out.Children {
		switch out.Children[i].IdempotencyKey {
		case "k-ok":
			ok = &out.Children[i]
		case "k-bad":
			bad = &out.Children[i]
		}
	}
	if ok == nil || ok.Status != StatusRunning {
		t.Fatalf("ok child wrong: %+v", ok)
	}
	if bad == nil || bad.Status != StatusFailed {
		t.Fatalf("bad child wrong: %+v", bad)
	}
}

// TestListSystemPoints_ServiceFilter: ?service=foo only returns points for
// that service.
func TestListSystemPoints_ServiceFilter(t *testing.T) {
	mgr, _, db := newTestManager(t)
	_, p1 := seedSystemAndPoint(t, db)

	rows, names, total, err := mgr.ListSystemPoints(t.Context(), ListPointsFilter{
		System: "ts", Service: "frontend",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("want 1 row got total=%d rows=%d", total, len(rows))
	}
	if rows[0].ID != p1 {
		t.Fatalf("wrong id: %s", rows[0].ID)
	}
	if names[*rows[0].ServiceID] != "frontend" {
		t.Fatalf("service-name lookup failed: %v", names)
	}

	// Unknown service → empty
	rows, _, total, err = mgr.ListSystemPoints(t.Context(), ListPointsFilter{
		System: "ts", Service: "nope",
	})
	if err != nil {
		t.Fatalf("list2: %v", err)
	}
	if total != 0 || len(rows) != 0 {
		t.Fatalf("unknown svc must be empty; got %d", total)
	}
}
