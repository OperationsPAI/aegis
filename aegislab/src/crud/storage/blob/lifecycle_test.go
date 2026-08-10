package blob

import (
	"context"
	"testing"
	"time"

	"aegis/platform/testutil"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func newLifecycleHarness(t *testing.T) (*DeletionWorker, *Service, *Repository, *mockS3, func()) {
	t.Helper()
	drv, mock, closeSrv := newTestDriver(t)
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&ObjectRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := NewRepository(db)
	cfg := BucketConfig{Name: "test", Driver: "s3"}
	reg := NewTestRegistry(map[string]*Bucket{
		"test": {Config: cfg, Driver: drv},
	})
	clk := fixedClock{t: time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)}
	svc := NewService(reg, repo, clk)
	w := &DeletionWorker{
		svc: svc, repo: repo, registry: reg, clock: clk,
		interval: time.Hour, batch: 100, orphanAge: time.Hour,
		grace: 24 * time.Hour,
	}
	return w, svc, repo, mock, closeSrv
}

// End-to-end "expire → soft-delete → driver delete → hard delete" is
// covered by the real-deployment verification documented in the PR
// description; the unit tests here cover the orphan-reconcile passes,
// which are awkward to exercise from outside the process.

func TestDeletionWorker_OrphanReconcile_Backfill(t *testing.T) {
	w, _, repo, mock, done := newLifecycleHarness(t)
	defer done()
	ctx := context.Background()

	mock.objects["orphan/k.bin"] = []byte("hello")
	mock.cts["orphan/k.bin"] = "application/octet-stream"
	rec := &ObjectRecord{
		Bucket: "test", StorageKey: "orphan/k.bin", EntityKind: "orphan",
	}
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := repo.DB.Model(&ObjectRecord{}).Where("id = ?", rec.ID).
		Update("created_at", w.clock.Now().Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("pin created_at: %v", err)
	}

	if err := w.Run(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	got, err := repo.FindByKey(ctx, "test", "orphan/k.bin")
	if err != nil {
		t.Fatalf("FindByKey: %v", err)
	}
	if got.SizeBytes != int64(len("hello")) {
		t.Fatalf("backfill size: got %d want %d", got.SizeBytes, len("hello"))
	}
}

func TestDeletionWorker_OrphanReconcile_Abandon(t *testing.T) {
	w, _, repo, _, done := newLifecycleHarness(t)
	defer done()
	ctx := context.Background()

	rec := &ObjectRecord{
		Bucket: "test", StorageKey: "orphan/never-uploaded", EntityKind: "orphan",
	}
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := repo.DB.Model(&ObjectRecord{}).Where("id = ?", rec.ID).
		Update("created_at", w.clock.Now().Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("pin created_at: %v", err)
	}

	if err := w.Run(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// After reconcile the orphan is soft-deleted; the grace window
	// keeps the metadata row until the next sweep past `grace`.
	var got ObjectRecord
	if err := repo.DB.Unscoped().Where("id = ?", rec.ID).First(&got).Error; err != nil {
		t.Fatalf("lookup after reconcile: %v", err)
	}
	if got.DeletedAt == nil {
		t.Fatalf("orphan should be soft-deleted, DeletedAt is nil")
	}

	w.clock = fixedClock{t: w.clock.Now().Add(w.grace + time.Minute)}
	w.deletePass(ctx, w.clock.Now())

	var count int64
	if err := repo.DB.Model(&ObjectRecord{}).Where("id = ?", rec.ID).
		Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("abandoned orphan still present after grace: count=%d", count)
	}
}

// TestDeletionWorker_GracePeriod pins the soft-delete cushion: a row
// soft-deleted "just now" must survive the sweep, while one whose
// deleted_at predates the grace window must be hard-deleted. Without
// the grace filter on deletePass, BucketLifecycleWorker's 24h cushion
// is effectively zero whenever both workers run.
func TestDeletionWorker_GracePeriod(t *testing.T) {
	w, _, repo, _, done := newLifecycleHarness(t)
	defer done()
	ctx := context.Background()

	fresh := &ObjectRecord{Bucket: "test", StorageKey: "fresh.bin", EntityKind: "blob"}
	if err := repo.Create(ctx, fresh); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}
	if err := repo.MarkExpired(ctx, fresh.ID, w.clock.Now()); err != nil {
		t.Fatalf("mark fresh: %v", err)
	}

	stale := &ObjectRecord{Bucket: "test", StorageKey: "stale.bin", EntityKind: "blob"}
	if err := repo.Create(ctx, stale); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if err := repo.MarkExpired(ctx, stale.ID, w.clock.Now().Add(-25*time.Hour)); err != nil {
		t.Fatalf("mark stale: %v", err)
	}

	w.deletePass(ctx, w.clock.Now())

	var freshCount, staleCount int64
	if err := repo.DB.Model(&ObjectRecord{}).Where("id = ?", fresh.ID).Count(&freshCount).Error; err != nil {
		t.Fatalf("count fresh: %v", err)
	}
	if err := repo.DB.Model(&ObjectRecord{}).Where("id = ?", stale.ID).Count(&staleCount).Error; err != nil {
		t.Fatalf("count stale: %v", err)
	}
	if freshCount != 1 {
		t.Fatalf("fresh row swept inside grace window: count=%d (want 1)", freshCount)
	}
	if staleCount != 0 {
		t.Fatalf("stale row past grace not swept: count=%d (want 0)", staleCount)
	}
}
