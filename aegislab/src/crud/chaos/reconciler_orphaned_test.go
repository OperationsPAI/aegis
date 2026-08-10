package chaos

import (
	"context"
	"testing"

	"aegis/platform/testutil"

	"gorm.io/gorm"
)

// statusFakeExecutor lets a test pin Status's return; everything else
// behaves like fakeExecutor.
type statusFakeExecutor struct {
	fakeExecutor
	statusState ExecState
	statusDiag  map[string]any
	statusErr   error
}

func (e *statusFakeExecutor) Status(ctx context.Context, handle string) (ExecState, map[string]any, error) {
	return e.statusState, e.statusDiag, e.statusErr
}

func newReconcilerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(
		&System{}, &Service{}, &ImportLock{}, &Capability{},
		&Point{}, &ExecutorRecord{}, &InjectionBatch{}, &Injection{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

// Regression: an in-flight injection whose chaos-mesh CR vanishes (manual
// `kubectl delete podchaos ...`, GC race) used to be promoted to
// `succeeded` because Status returned ExecStateSucceeded for cr_absent.
// Downstream BuildDatapack then ran with no fault. The executor now
// returns ExecStateOrphaned and the reconciler marks the row failed.
func TestReconciler_OrphanedDoesNotPromoteRunningToSucceeded(t *testing.T) {
	db := newReconcilerTestDB(t)
	inj := Injection{
		ID:             "inj-orphan-running",
		PointID:        "p1",
		Params:         JSONMap{},
		IdempotencyKey: "idem-orphan-running",
		ExecutorName:   "fake",
		Status:         StatusRunning,
		ExecutorHandle: `{"name":"x","namespace":"ns","gvr":"fake"}`,
	}
	if err := db.Create(&inj).Error; err != nil {
		t.Fatalf("create inj: %v", err)
	}

	exec := &statusFakeExecutor{statusState: ExecStateOrphaned, statusDiag: map[string]any{"reason": "cr_absent"}}
	r := NewReconciler(db, exec, nil, 0, nil)
	r.reconcileOne(context.Background(), &inj)

	var got Injection
	if err := db.Where("id = ?", inj.ID).Take(&got).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status == StatusSucceeded {
		t.Fatalf("running row was promoted to succeeded on cr_absent (regression)")
	}
	if got.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.FinishedAt == nil {
		t.Fatalf("expected finished_at set")
	}
}

// Terminal-prior rows (succeeded/failed/cancelled) calling Status() after
// Destroy legitimately see cr_absent. The reconciler must not flip them.
func TestReconciler_OrphanedLeavesTerminalRowsAlone(t *testing.T) {
	db := newReconcilerTestDB(t)
	inj := Injection{
		ID:             "inj-orphan-succ",
		PointID:        "p1",
		Params:         JSONMap{},
		IdempotencyKey: "idem-orphan-succ",
		ExecutorName:   "fake",
		Status:         StatusSucceeded,
		ExecutorHandle: `{"name":"x","namespace":"ns","gvr":"fake"}`,
	}
	if err := db.Create(&inj).Error; err != nil {
		t.Fatalf("create inj: %v", err)
	}

	exec := &statusFakeExecutor{statusState: ExecStateOrphaned, statusDiag: map[string]any{"reason": "cr_absent"}}
	r := NewReconciler(db, exec, nil, 0, nil)
	r.reconcileOne(context.Background(), &inj)

	var got Injection
	if err := db.Where("id = ?", inj.ID).Take(&got).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != StatusSucceeded {
		t.Fatalf("terminal row should not transition; got %s", got.Status)
	}
}
