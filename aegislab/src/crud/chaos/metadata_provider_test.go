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
