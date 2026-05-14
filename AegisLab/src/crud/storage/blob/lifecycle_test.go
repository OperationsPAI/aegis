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

	var count int64
	if err := repo.DB.Model(&ObjectRecord{}).Where("id = ?", rec.ID).
		Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("abandoned orphan still present: count=%d", count)
	}
}
