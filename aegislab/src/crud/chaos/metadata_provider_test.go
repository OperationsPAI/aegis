package chaos

import (
	"context"
	"testing"
	"time"
)

// TestChaosPointStore_QueryPoints_FiltersBySystemAndStatus seeds two
// systems plus one superseded row and asserts the DB-backed store returns
// only the active rows for the queried system.
func TestChaosPointStore_QueryPoints_FiltersBySystemAndStatus(t *testing.T) {
	_, _, db := newTestManager(t)

	now := time.Now()
	seed := func(id, system, capability string, target JSONMap, status string) {
		t.Helper()
		if err := db.Create(&Point{
			ID: id, SystemName: system, CapabilityName: capability,
			Target: target, Source: "test", Status: status,
			CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("ts0000000000aaaa", "ts", "http_response_abort",
		JSONMap{"app": "ts-user", "method": "GET", "path": "/u", "port": float64(8080)}, PointActive)
	seed("ts0000000000bbbb", "ts", "network_delay",
		JSONMap{"source_app": "ts-user", "target_service": "ts-auth"}, PointActive)
	seed("ts0000000000cccc", "ts", "http_response_delay",
		JSONMap{"app": "ts-user", "method": "GET", "path": "/u", "port": float64(8080)}, PointSuperseded)
	seed("od0000000000aaaa", "otel-demo", "http_response_abort",
		JSONMap{"app": "checkout", "method": "POST", "path": "/ship", "port": float64(8080)}, PointActive)

	store := NewChaosPointStore(db)
	rows, err := store.QueryPoints(context.Background(), "ts")
	if err != nil {
		t.Fatalf("QueryPoints: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 active ts rows (superseded excluded, otel-demo excluded), got %d: %#v", len(rows), rows)
	}
	caps := map[string]bool{rows[0].CapabilityName: true, rows[1].CapabilityName: true}
	if !caps["http_response_abort"] || !caps["network_delay"] {
		t.Errorf("unexpected capabilities: %#v", rows)
	}
	for _, r := range rows {
		if r.SystemName != "ts" {
			t.Errorf("cross-system bleed: %#v", r)
		}
	}

	RegisterChaosPointStore(db)
}

// TestChaosPointStore_LatestUpdate_TracksAllStatusTransitions proves the
// MAX(updated_at) probe driving cross-process cache invalidation surfaces
// imports, supersedes, and per-system isolation. The empty case is required
// by the issue's "first-miss" edge case enumeration.
func TestChaosPointStore_LatestUpdate_TracksAllStatusTransitions(t *testing.T) {
	_, _, db := newTestManager(t)
	store := NewChaosPointStore(db)
	ctx := context.Background()

	// Empty system → zero time, no error.
	got, err := store.LatestUpdate(ctx, "ts")
	if err != nil {
		t.Fatalf("LatestUpdate (empty): %v", err)
	}
	if !got.IsZero() {
		t.Errorf("empty system: want zero time, got %v", got)
	}

	t0 := time.Now().UTC().Truncate(time.Second)
	if err := db.Create(&Point{
		ID: "ts0000000000aaaa", SystemName: "ts", CapabilityName: "http_response_abort",
		Target:    JSONMap{"app": "ts-user", "method": "GET", "path": "/u", "port": float64(8080)},
		Source:    "test",
		Status:    PointActive,
		CreatedAt: t0, UpdatedAt: t0,
	}).Error; err != nil {
		t.Fatalf("seed ts0000000000aaaa: %v", err)
	}

	got, err = store.LatestUpdate(ctx, "ts")
	if err != nil {
		t.Fatalf("LatestUpdate (one row): %v", err)
	}
	if !got.Equal(t0) {
		t.Errorf("want MAX=%v, got %v", t0, got)
	}

	// Per-system isolation: probing another system must not see ts's rows.
	got, err = store.LatestUpdate(ctx, "otel-demo")
	if err != nil {
		t.Fatalf("LatestUpdate (other system): %v", err)
	}
	if !got.IsZero() {
		t.Errorf("cross-system bleed: want zero, got %v", got)
	}

	// Supersede flips status — autoUpdateTime bumps updated_at, probe catches it.
	t1 := t0.Add(time.Minute)
	if err := db.Model(&Point{}).Where("id = ?", "ts0000000000aaaa").
		Updates(map[string]any{"status": PointSuperseded, "updated_at": t1}).Error; err != nil {
		t.Fatalf("supersede: %v", err)
	}
	got, err = store.LatestUpdate(ctx, "ts")
	if err != nil {
		t.Fatalf("LatestUpdate (after supersede): %v", err)
	}
	if !got.Equal(t1) {
		t.Errorf("supersede should advance MAX to %v, got %v", t1, got)
	}
}
