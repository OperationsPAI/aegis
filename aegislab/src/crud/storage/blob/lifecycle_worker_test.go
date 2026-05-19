package blob

import (
	"context"
	"sync"
	"testing"
	"time"

	redisinfra "aegis/platform/redis"
	"aegis/platform/testutil"
)

// newBucketLifecycleHarness wires a worker with no Redis (single-replica
// path), an in-memory SQLite DB, and a fixed clock so tests can pin the
// expiry boundary.
func newBucketLifecycleHarness(t *testing.T, lc *BucketLifecycle) (*BucketLifecycleWorker, *Repository, *Registry, fixedClock) {
	t.Helper()
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&ObjectRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := NewRepository(db)
	cfg := BucketConfig{Name: "test", Driver: "s3", Lifecycle: lc}
	reg := NewTestRegistry(map[string]*Bucket{
		"test": {Config: cfg, Driver: nil},
	})
	clk := fixedClock{t: time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)}
	w := &BucketLifecycleWorker{
		repo: repo, registry: reg, clock: clk,
		interval: time.Hour, grace: 24 * time.Hour, batch: 500,
		lockTTL: 10 * time.Minute,
	}
	return w, repo, reg, clk
}

// seedObject inserts a single object with a back-dated created_at so the
// predicate test can drive the age boundary without sleeping.
func seedObject(t *testing.T, repo *Repository, key string, ageDays int, clk fixedClock) int64 {
	t.Helper()
	ctx := context.Background()
	rec := &ObjectRecord{Bucket: "test", StorageKey: key}
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("seed %q: %v", key, err)
	}
	if ageDays > 0 {
		backdated := clk.Now().Add(-time.Duration(ageDays) * 24 * time.Hour).Add(-time.Minute)
		if err := repo.DB.Model(&ObjectRecord{}).Where("id = ?", rec.ID).
			Update("created_at", backdated).Error; err != nil {
			t.Fatalf("backdate %q: %v", key, err)
		}
	}
	return rec.ID
}

// TestBucketLifecycleWorker_Predicate covers the matrix the worker
// must get right: prefix matching, age boundary, soft-deleted exclusion,
// and the "empty prefix = whole bucket" case. One test, parametrized,
// because the predicate is the load-bearing piece and we want
// regressions on any cell to fail loudly.
func TestBucketLifecycleWorker_Predicate(t *testing.T) {
	lc := &BucketLifecycle{
		Rules: []BucketLifecycleRule{
			{Name: "expire-tmp", MatchPrefix: "tmp/", ExpireAfterDays: 7, Action: "delete"},
			{Name: "expire-logs", MatchPrefix: "logs/", ExpireAfterDays: 30, Action: "delete"},
		},
	}
	w, repo, _, clk := newBucketLifecycleHarness(t, lc)
	ctx := context.Background()

	// Should be swept: matches "tmp/" prefix AND older than 7 days.
	stale := seedObject(t, repo, "tmp/stale.bin", 10, clk)
	// Should NOT be swept: matches prefix but younger than 7 days.
	fresh := seedObject(t, repo, "tmp/fresh.bin", 3, clk)
	// Should NOT be swept: old but does not match either prefix.
	other := seedObject(t, repo, "keep/old.bin", 100, clk)
	// Should be swept by "expire-logs": matches "logs/" AND > 30 days.
	staleLog := seedObject(t, repo, "logs/2024/jan.log", 60, clk)
	// Should NOT be swept: 60 days old, matches "logs/", but already
	// soft-deleted — the worker must not double-process.
	already := seedObject(t, repo, "logs/2024/feb.log", 60, clk)
	if err := repo.MarkExpired(ctx, already, clk.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("pre-soft-delete: %v", err)
	}

	res, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Buckets) != 1 {
		t.Fatalf("buckets reported = %d, want 1: %+v", len(res.Buckets), res.Buckets)
	}
	if got, want := res.Buckets[0].TotalKeys, 2; got != want {
		t.Fatalf("swept keys = %d, want %d (stale tmp + stale log); rules=%+v", got, want, res.Buckets[0].Rules)
	}

	mustDeleted := func(id int64, key string) {
		t.Helper()
		var rec ObjectRecord
		if err := repo.DB.Unscoped().Where("id = ?", id).First(&rec).Error; err != nil {
			t.Fatalf("lookup %s: %v", key, err)
		}
		if rec.DeletedAt == nil {
			t.Fatalf("%s should be soft-deleted but DeletedAt is nil", key)
		}
	}
	mustAlive := func(id int64, key string) {
		t.Helper()
		var rec ObjectRecord
		if err := repo.DB.Unscoped().Where("id = ?", id).First(&rec).Error; err != nil {
			t.Fatalf("lookup %s: %v", key, err)
		}
		if rec.DeletedAt != nil {
			t.Fatalf("%s should be alive but DeletedAt=%v", key, rec.DeletedAt)
		}
	}

	mustDeleted(stale, "tmp/stale.bin")
	mustAlive(fresh, "tmp/fresh.bin")
	mustAlive(other, "keep/old.bin")
	mustDeleted(staleLog, "logs/2024/jan.log")
	mustDeleted(already, "logs/2024/feb.log") // already soft-deleted; remains so
}

// TestBucketLifecycleWorker_Lock simulates two replicas calling Run
// concurrently against a shared fake Redis. Only one acquires the lock
// and performs work; the other gets `LockHeld=false` and walks away
// without touching the DB.
func TestBucketLifecycleWorker_Lock(t *testing.T) {
	// Fake Redis: a single shared map with mutex, honouring SetNX
	// semantics + compare-and-delete on release.
	type fakeRedis struct {
		mu sync.Mutex
		kv map[string]string
	}
	fake := &fakeRedis{kv: map[string]string{}}

	origSetNX := BucketLifecycleSetNXFn
	origDel := BucketLifecycleCompareDelFn
	BucketLifecycleSetNXFn = func(ctx context.Context, gw *redisinfra.Gateway, key, value string, ttl time.Duration) (bool, error) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if _, exists := fake.kv[key]; exists {
			return false, nil
		}
		fake.kv[key] = value
		return true, nil
	}
	BucketLifecycleCompareDelFn = func(ctx context.Context, gw *redisinfra.Gateway, key, value string) (int64, error) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if fake.kv[key] != value {
			return 0, nil
		}
		delete(fake.kv, key)
		return 1, nil
	}
	t.Cleanup(func() {
		BucketLifecycleSetNXFn = origSetNX
		BucketLifecycleCompareDelFn = origDel
	})

	lc := &BucketLifecycle{
		Rules: []BucketLifecycleRule{
			{Name: "expire-all", MatchPrefix: "", ExpireAfterDays: 1, Action: "delete"},
		},
	}
	wA, repoA, _, clk := newBucketLifecycleHarness(t, lc)
	// Share the same DB across both workers so we can observe whether
	// the "loser" replica touched any row.
	wB := &BucketLifecycleWorker{
		repo: repoA, registry: wA.registry, clock: clk,
		interval: time.Hour, grace: 24 * time.Hour, batch: 500,
		lockTTL: 10 * time.Minute,
	}
	// Inject the same non-nil sentinel Gateway so the workers take the
	// "redis wired" path through the seams.
	sentinel := &redisinfra.Gateway{}
	wA.redis = sentinel
	wB.redis = sentinel

	// Hold the lock from "replica A" by acquiring + NOT releasing it
	// until after replica B has tried. We do this by stalling A inside
	// its sweep using a channel-gated rule.
	startedA := make(chan struct{})
	finishB := make(chan struct{})
	doneA := make(chan *LifecycleSweepResult, 1)
	doneB := make(chan *LifecycleSweepResult, 1)

	// Replica A: hand-rolled lock-then-block sequence so we know A holds
	// the lock when B's Run hits SetNX.
	go func() {
		ok, release, err := wA.acquireLock(context.Background())
		if err != nil || !ok {
			t.Errorf("replica A failed to acquire lock: ok=%v err=%v", ok, err)
			close(startedA)
			doneA <- nil
			return
		}
		close(startedA)
		<-finishB
		release()
		doneA <- &LifecycleSweepResult{LockHeld: true}
	}()
	<-startedA

	// Replica B: should observe lock held and return LockHeld=false.
	go func() {
		res, err := wB.Run(context.Background())
		if err != nil {
			t.Errorf("replica B Run: %v", err)
		}
		doneB <- res
	}()

	resB := <-doneB
	if resB == nil {
		t.Fatal("replica B returned nil result")
	}
	if resB.LockHeld {
		t.Fatalf("replica B reported LockHeld=true, expected false (replica A holds lock); result=%+v", resB)
	}
	if len(resB.Buckets) != 0 {
		t.Fatalf("replica B touched %d buckets, expected 0", len(resB.Buckets))
	}

	close(finishB)
	<-doneA

	// After A releases, the lock map must be empty (compare-and-delete
	// hit the right value).
	fake.mu.Lock()
	if _, exists := fake.kv[bucketLifecycleSweepLockKey]; exists {
		fake.mu.Unlock()
		t.Fatal("lock key still present after replica A released")
	}
	fake.mu.Unlock()
}
