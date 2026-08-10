package chaos

import (
	"context"
	"sort"
	"testing"
)

// TestExportSystemPoints_RoundTripsThroughImport seeds a fresh DB with chaos
// rows across two services, calls ExportSystemPoints, then feeds the result
// back through ImportPoints into a separate manager and asserts the
// resulting PointID set is identical. This is the only test that proves the
// export/import primitive is actually reversible.
func TestExportSystemPoints_RoundTripsThroughImport(t *testing.T) {
	srcMgr, _, _ := newTestManager(t)
	if err := srcMgr.UpsertSystem(context.Background(), &System{
		Name: "otel-demo", NsPattern: "otel-demo", AppLabelKey: "app", Enabled: true,
	}); err != nil {
		t.Fatalf("upsert src system: %v", err)
	}

	// Seed: two services (cart, checkout), three points across two
	// capabilities, plus one superseded row that the default export must drop.
	if _, err := srcMgr.ImportPoints(context.Background(), "otel-demo", PointManifest{
		APIVersion: "aegis-chaos/v1beta",
		Kind:       "PointManifest",
		Metadata: PointManifestMetadata{
			System: "otel-demo", Service: "cart", Instance: "default", ChartVersion: "v1",
		},
		Spec: PointManifestSpec{
			ReplaceScope: ReplaceScopeService,
			Points: []PointManifestEntry{
				{Capability: "http_response_abort", Target: map[string]any{
					"namespace": "otel-demo", "app": "cart", "method": "GET",
					"path": "/cart", "port": float64(8080),
				}},
				{Capability: "http_response_delay", Target: map[string]any{
					"namespace": "otel-demo", "app": "cart", "method": "GET",
					"path": "/cart", "port": float64(8080),
				}},
			},
		},
	}, false); err != nil {
		t.Fatalf("seed cart: %v", err)
	}
	if _, err := srcMgr.ImportPoints(context.Background(), "otel-demo", PointManifest{
		APIVersion: "aegis-chaos/v1beta",
		Kind:       "PointManifest",
		Metadata: PointManifestMetadata{
			System: "otel-demo", Service: "checkout", Instance: "default", ChartVersion: "v1",
		},
		Spec: PointManifestSpec{
			ReplaceScope: ReplaceScopeService,
			Points: []PointManifestEntry{
				{Capability: "network_delay", Target: map[string]any{
					"namespace": "otel-demo", "source_app": "checkout", "target_service": "payment",
				}},
			},
		},
	}, false); err != nil {
		t.Fatalf("seed checkout: %v", err)
	}
	// Mark one row superseded to confirm the default export excludes it.
	if err := srcMgr.DB.Model(&Point{}).
		Where("system_name = ? AND capability_name = ?", "otel-demo", "http_response_delay").
		Update("status", PointSuperseded).Error; err != nil {
		t.Fatalf("supersede: %v", err)
	}

	srcIDs := activePointIDs(t, srcMgr, "otel-demo")
	if len(srcIDs) != 2 {
		t.Fatalf("want 2 active rows post-seed, got %d", len(srcIDs))
	}

	manifests, err := srcMgr.ExportSystemPoints(context.Background(), "otel-demo", false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(manifests) != 2 {
		t.Fatalf("want 2 manifests (cart + checkout), got %d", len(manifests))
	}

	// Import into a fresh manager and confirm PointID set matches.
	dstMgr, _, _ := newTestManager(t)
	if err := dstMgr.UpsertSystem(context.Background(), &System{
		Name: "otel-demo", NsPattern: "otel-demo", AppLabelKey: "app", Enabled: true,
	}); err != nil {
		t.Fatalf("upsert dst system: %v", err)
	}
	for _, m := range manifests {
		if _, err := dstMgr.ImportPoints(context.Background(), "otel-demo", m, false); err != nil {
			t.Fatalf("re-import %s/%s: %v", m.Metadata.Service, m.Metadata.ChartVersion, err)
		}
	}
	dstIDs := activePointIDs(t, dstMgr, "otel-demo")
	if !sameStringSet(srcIDs, dstIDs) {
		t.Errorf("round-trip lost rows.\n  src: %v\n  dst: %v", srcIDs, dstIDs)
	}

	// Include-superseded mode surfaces the third row in a separate manifest
	// or extra entry; just check the count climbs.
	manifestsAll, err := srcMgr.ExportSystemPoints(context.Background(), "otel-demo", true)
	if err != nil {
		t.Fatalf("export include-superseded: %v", err)
	}
	totalEntries := 0
	for _, m := range manifestsAll {
		totalEntries += len(m.Spec.Points)
	}
	if totalEntries != 3 {
		t.Errorf("include_superseded=true: want 3 total entries (2 active + 1 superseded), got %d", totalEntries)
	}
}

func activePointIDs(t *testing.T, m *Manager, system string) []string {
	t.Helper()
	var rows []Point
	if err := m.DB.Where("system_name = ? AND status = ?", system, PointActive).Find(&rows).Error; err != nil {
		t.Fatalf("query active points: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	return ids
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestExportSystemPoints_EmptySystem returns the zero-manifest envelope
// rather than nil or error — clients can iterate.
func TestExportSystemPoints_EmptySystem(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	got, err := mgr.ExportSystemPoints(context.Background(), "no-such-system", false)
	if err != nil {
		t.Fatalf("export empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 manifests for empty system, got %d", len(got))
	}
}

