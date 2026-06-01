package chaos

import (
	"fmt"
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

// TestImportPoints_DryRun_ReportsWouldBeSuperseded: the would-deprecate
// preview in `manifest reconcile-dir --dry-run` over-counts unless import
// reports which active points it would supersede. A committed re-import
// supersedes the drifted point; a dry-run rolls back, so the point stays
// active in any later listing — so the dry-run result must still enumerate
// that id under SupersededIDs, letting the CLI subtract it instead of
// counting it as a deprecation. Asserts the dry-run set matches the
// committed set exactly.
func TestImportPoints_DryRun_ReportsWouldBeSuperseded(t *testing.T) {
	mgr := newImportTestManager(t)

	first := baseManifest()
	first.Spec.Points = []PointManifestEntry{
		{Capability: "pod_kill", Target: map[string]any{"namespace": "ts", "app": "frontend"}},
		{Capability: "pod_kill", Target: map[string]any{"namespace": "ts", "app": "frontend-v2"}},
	}
	firstRes, err := mgr.ImportPoints(t.Context(), "ts", first, false)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	// Re-import frontend with only the first point — the v2 point drifts out
	// and is the one that would be superseded.
	second := baseManifest()
	keptID := firstRes.PointIDs[0]
	wantSuperseded := firstRes.PointIDs[1]

	dryRes, err := mgr.ImportPoints(t.Context(), "ts", second, true)
	if err != nil {
		t.Fatalf("dry-run re-import: %v", err)
	}
	if dryRes.PointIDs[0] != keptID {
		t.Fatalf("dry-run kept id = %s, want %s", dryRes.PointIDs[0], keptID)
	}
	if got := dryRes.SupersededIDs; len(got) != 1 || got[0] != wantSuperseded {
		t.Fatalf("dry-run SupersededIDs = %v, want [%s]", got, wantSuperseded)
	}

	// The dry-run rolled back: the drifted point is still active, so a
	// would-deprecate preview listing active points would see it. The fix is
	// for the CLI to subtract SupersededIDs — confirm the row stayed active.
	var status string
	if err := mgr.DB.Model(&Point{}).Select("status").
		Where("id = ?", wantSuperseded).Take(&status).Error; err != nil {
		t.Fatalf("lookup drifted point: %v", err)
	}
	if status != PointActive {
		t.Fatalf("dry-run must leave drifted point active; got %q", status)
	}

	// A committed re-import supersedes exactly that id — proving the dry-run
	// preview matched real behaviour.
	commitRes, err := mgr.ImportPoints(t.Context(), "ts", second, false)
	if err != nil {
		t.Fatalf("committed re-import: %v", err)
	}
	if got := commitRes.SupersededIDs; len(got) != 1 || got[0] != wantSuperseded {
		t.Fatalf("committed SupersededIDs = %v, want [%s]", got, wantSuperseded)
	}
}

// TestImportPoints_MultiCapabilityManifest_AllActive pins the contract the
// reconcile-dir merge relies on: one replace_scope=service manifest carrying
// disjoint capability families (http + network) under the same service keeps
// every point active. The #505 regression came from importing those families
// as separate files, each clobbering the other's points; merging them into one
// manifest — which is what reconcile-dir now does — is correct only if a single
// multi-capability manifest activates them all.
func TestImportPoints_MultiCapabilityManifest_AllActive(t *testing.T) {
	mgr := newImportTestManager(t)

	m := baseManifest()
	m.Spec.Points = []PointManifestEntry{
		{Capability: "pod_kill", Target: map[string]any{"namespace": "ts", "app": "frontend"}},
		{Capability: "pod_failure", Target: map[string]any{"namespace": "ts", "app": "frontend"}},
	}
	res, err := mgr.ImportPoints(t.Context(), "ts", m, false)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	for _, id := range res.PointIDs {
		var status string
		if err := mgr.DB.Model(&Point{}).Select("status").
			Where("id = ?", id).Take(&status).Error; err != nil {
			t.Fatalf("lookup point %s: %v", id, err)
		}
		if status != PointActive {
			t.Fatalf("point %s not active after merged import; got %q", id, status)
		}
	}

	var active int64
	mgr.DB.Model(&Point{}).Where("status = ?", PointActive).Count(&active)
	if active != 2 {
		t.Fatalf("expected 2 active points, got %d", active)
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

// TestImportPoints_BatchUpsert_LargeManifest exercises the batched UPSERT
// path: importing 500 points in one manifest must persist all of them in
// a single CreateInBatches call and the result must enumerate every
// PointID. The Upserted counter is dialect-dependent (SQLite reports 1
// per row, MySQL up to 2 for replaced rows) so we only assert it matches
// the count of rows actually persisted.
func TestImportPoints_BatchUpsert_LargeManifest(t *testing.T) {
	mgr := newImportTestManager(t)

	m := baseManifest()
	pts := make([]PointManifestEntry, 0, 500)
	for i := 0; i < 500; i++ {
		pts = append(pts, PointManifestEntry{
			Capability: "pod_kill",
			Target: map[string]any{
				"namespace": "ts",
				"app":       fmt.Sprintf("frontend-%04d", i),
			},
		})
	}
	m.Spec.Points = pts
	m.Spec.ReplaceScope = ReplaceScopeNone

	res, err := mgr.ImportPoints(t.Context(), "ts", m, false)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(res.PointIDs) != 500 {
		t.Fatalf("expected 500 PointIDs, got %d", len(res.PointIDs))
	}

	var active int64
	mgr.DB.Model(&Point{}).Where("status = ?", PointActive).Count(&active)
	if active != 500 {
		t.Fatalf("expected 500 active points persisted, got %d", active)
	}
	if res.Upserted != int(active) {
		t.Fatalf("Upserted counter should equal rows persisted on SQLite; got upserted=%d active=%d",
			res.Upserted, active)
	}

	// Re-import the same manifest: every row hits the ON CONFLICT branch
	// and updates rather than inserts. PointIDs must still enumerate all
	// 500, and the active row count stays at 500.
	res2, err := mgr.ImportPoints(t.Context(), "ts", m, false)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if len(res2.PointIDs) != 500 {
		t.Fatalf("re-import PointIDs: want 500, got %d", len(res2.PointIDs))
	}
	mgr.DB.Model(&Point{}).Where("status = ?", PointActive).Count(&active)
	if active != 500 {
		t.Fatalf("after re-import expected 500 active, got %d", active)
	}
}

// TestSweepPoints_DeprecatesAbsent: after importing two services' worth of
// active points, sweeping with a subset of their ids deprecates every active
// point not in the set — across services — while the kept ids stay active.
func TestSweepPoints_DeprecatesAbsent(t *testing.T) {
	mgr := newImportTestManager(t)

	frontend := baseManifest()
	frontend.Spec.ReplaceScope = ReplaceScopeNone
	frontend.Spec.Points = []PointManifestEntry{
		{Capability: "pod_kill", Target: map[string]any{"namespace": "ts", "app": "frontend"}},
		{Capability: "pod_failure", Target: map[string]any{"namespace": "ts", "app": "frontend"}},
	}
	fRes, err := mgr.ImportPoints(t.Context(), "ts", frontend, false)
	if err != nil {
		t.Fatalf("import frontend: %v", err)
	}

	cart := PointManifest{
		APIVersion: "aegis-chaos/v1beta", Kind: "PointManifest",
		Metadata: PointManifestMetadata{System: "ts", Service: "cart", ChartVersion: "v1.0.0"},
		Spec: PointManifestSpec{
			ReplaceScope: ReplaceScopeNone,
			Points: []PointManifestEntry{
				{Capability: "pod_kill", Target: map[string]any{"namespace": "ts", "app": "cart"}},
			},
		},
	}
	cRes, err := mgr.ImportPoints(t.Context(), "ts", cart, false)
	if err != nil {
		t.Fatalf("import cart: %v", err)
	}

	// Keep only the first frontend point + the cart point active.
	keep := []string{fRes.PointIDs[0], cRes.PointIDs[0]}
	deprecated, err := mgr.SweepPoints(t.Context(), "ts", keep)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if deprecated != 1 {
		t.Fatalf("expected 1 deprecated (the second frontend point), got %d", deprecated)
	}

	var active, dep int64
	mgr.DB.Model(&Point{}).Where("status = ?", PointActive).Count(&active)
	mgr.DB.Model(&Point{}).Where("status = ?", PointDeprecated).Count(&dep)
	if active != 2 || dep != 1 {
		t.Fatalf("expected active=2 deprecated=1; got active=%d deprecated=%d", active, dep)
	}

	// Idempotent: a second sweep with the same kept set deprecates nothing.
	again, err := mgr.SweepPoints(t.Context(), "ts", keep)
	if err != nil {
		t.Fatalf("re-sweep: %v", err)
	}
	if again != 0 {
		t.Fatalf("re-sweep should deprecate 0, got %d", again)
	}
}

// TestSweepPoints_RejectsEmptySet: an empty active_point_ids set must be
// refused so a caller can't accidentally deprecate the whole system.
func TestSweepPoints_RejectsEmptySet(t *testing.T) {
	mgr := newImportTestManager(t)
	m := baseManifest()
	m.Spec.ReplaceScope = ReplaceScopeNone
	if _, err := mgr.ImportPoints(t.Context(), "ts", m, false); err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := mgr.SweepPoints(t.Context(), "ts", nil); err == nil {
		t.Fatalf("expected empty-set rejection")
	}
	var active int64
	mgr.DB.Model(&Point{}).Where("status = ?", PointActive).Count(&active)
	if active != 1 {
		t.Fatalf("rejected sweep must not touch points; active=%d", active)
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
