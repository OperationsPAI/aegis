package chaos

import (
	"testing"
)

func newImportTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr, _, db := newTestManager(t)
	sys := System{Name: "ts", NsPattern: "ts*", AppLabelKey: "app", Enabled: true, MaxConcurrentInjections: 5}
	if err := db.Create(&sys).Error; err != nil {
		t.Fatalf("create system: %v", err)
	}
	return mgr
}

func baseManifest() PointManifest {
	return PointManifest{
		APIVersion: "aegis-chaos/v1beta",
		Kind:       "PointManifest",
		Metadata: PointManifestMetadata{
			System: "ts", Service: "frontend", ChartVersion: "v1.0.0",
		},
		Spec: PointManifestSpec{
			ReplaceScope: ReplaceScopeService,
			Points: []PointManifestEntry{
				{Capability: "pod_kill", Target: map[string]any{"namespace": "ts", "app": "frontend"}},
			},
		},
	}
}

// TestImportPoints_DryRunRollback: dry_run runs the full validation but
// must leave no Point row, no Service row, and no import_locks row
// behind — the tx rollback releases the lock.
func TestImportPoints_DryRunRollback(t *testing.T) {
	mgr := newImportTestManager(t)
	m := baseManifest()

	res, err := mgr.ImportPoints(t.Context(), "ts", m, true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !res.DryRun {
		t.Fatalf("DryRun flag missing on result")
	}
	if res.Upserted != 1 {
		t.Fatalf("dry-run should still report 1 would-be upsert; got %d", res.Upserted)
	}

	var pCount, svcCount, lockCount int64
	mgr.DB.Model(&Point{}).Count(&pCount)
	mgr.DB.Model(&Service{}).Count(&svcCount)
	mgr.DB.Model(&ImportLock{}).Count(&lockCount)
	if pCount != 0 || svcCount != 0 || lockCount != 0 {
		t.Fatalf("dry-run wrote rows: points=%d services=%d locks=%d",
			pCount, svcCount, lockCount)
	}
}

// TestImportPoints_ReplaceScopeService_Supersedes: re-importing for the
// same (system, service, instance) with a payload that omits a previous
// Point marks the previous one `superseded`. Points belonging to a
// different service must NOT be touched.
func TestImportPoints_ReplaceScopeService_Supersedes(t *testing.T) {
	mgr := newImportTestManager(t)

	first := baseManifest()
	first.Spec.Points = []PointManifestEntry{
		{Capability: "pod_kill", Target: map[string]any{"namespace": "ts", "app": "frontend"}},
		{Capability: "pod_kill", Target: map[string]any{"namespace": "ts", "app": "frontend-v2"}},
	}
	if _, err := mgr.ImportPoints(t.Context(), "ts", first, false); err != nil {
		t.Fatalf("first import: %v", err)
	}

	// Seed an unrelated Service+Point on a different service in the same
	// system — it must survive the replace-scope=service supersede.
	other := PointManifest{
		APIVersion: "aegis-chaos/v1beta", Kind: "PointManifest",
		Metadata: PointManifestMetadata{System: "ts", Service: "cart", ChartVersion: "v1.0.0"},
		Spec: PointManifestSpec{
			ReplaceScope: ReplaceScopeService,
			Points: []PointManifestEntry{
				{Capability: "pod_kill", Target: map[string]any{"namespace": "ts", "app": "cart"}},
			},
		},
	}
	if _, err := mgr.ImportPoints(t.Context(), "ts", other, false); err != nil {
		t.Fatalf("other import: %v", err)
	}

	// Re-import frontend with only the first Point — the v2 Point must
	// transition to `superseded`. cart's Point is untouched.
	second := baseManifest()
	res, err := mgr.ImportPoints(t.Context(), "ts", second, false)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if res.Superseded != 1 {
		t.Fatalf("expected 1 superseded, got %d", res.Superseded)
	}

	var active, superseded int64
	mgr.DB.Model(&Point{}).Where("status = ?", PointActive).Count(&active)
	mgr.DB.Model(&Point{}).Where("status = ?", PointSuperseded).Count(&superseded)
	if active != 2 || superseded != 1 {
		t.Fatalf("expected active=2 superseded=1; got active=%d superseded=%d", active, superseded)
	}

	var cartPoint Point
	if err := mgr.DB.Joins("JOIN chaos_services s ON s.id = chaos_points.service_id").
		Where("s.name = ?", "cart").Take(&cartPoint).Error; err != nil {
		t.Fatalf("cart point lookup: %v", err)
	}
	if cartPoint.Status != PointActive {
		t.Fatalf("cart point must stay active; got %q", cartPoint.Status)
	}
}

func TestImportPoints_RejectsReplaceScopeSystem(t *testing.T) {
	mgr := newImportTestManager(t)
	m := baseManifest()
	m.Spec.ReplaceScope = ReplaceScopeSystem
	if _, err := mgr.ImportPoints(t.Context(), "ts", m, false); err == nil {
		t.Fatalf("expected step-1 rejection of replace_scope=system")
	}
}

func TestValidateManifest_NormalisesInstance(t *testing.T) {
	m := baseManifest()
	m.Metadata.Instance = ""
	if err := validateManifest("ts", &m); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if m.Metadata.Instance != "default" {
		t.Fatalf("instance not normalised; got %q", m.Metadata.Instance)
	}
}
