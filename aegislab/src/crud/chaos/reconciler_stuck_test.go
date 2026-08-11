package chaos

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func newStuckReconciler(db *gorm.DB, exec Executor, webhook *WebhookSender, now time.Time) *Reconciler {
	r := NewReconciler(db, exec, webhook, 0, nil)
	r.now = func() time.Time { return now }
	return r
}

// captureWebhook stands up an httptest backend that records the last
// posted singleton payload, and a WebhookSender pointed at it.
type captureWebhook struct {
	mu      sync.Mutex
	srv     *httptest.Server
	gotBody webhookPayload
	hits    int
}

func newCaptureWebhook(t *testing.T) *captureWebhook {
	t.Helper()
	cw := &captureWebhook{}
	cw.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cw.mu.Lock()
		_ = json.Unmarshal(b, &cw.gotBody)
		cw.hits++
		cw.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(cw.srv.Close)
	return cw
}

func (cw *captureWebhook) sender(db *gorm.DB) *WebhookSender {
	return NewWebhookSender(cw.srv.Client(), cw.srv.URL, db, nil)
}

func (cw *captureWebhook) snapshot() (webhookPayload, int) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return cw.gotBody, cw.hits
}

// A running injection whose CR has been Pending past its expected
// completion (start + duration + grace) must be force-failed and the failed
// completion webhook fired.
func TestReconciler_StuckPendingForceFailsAndFiresWebhook(t *testing.T) {
	db := newReconcilerTestDB(t)
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	inj := Injection{
		ID:             "inj-stuck",
		PointID:        "p1",
		Params:         JSONMap{"duration_s": float64(60)},
		IdempotencyKey: "idem-stuck",
		ExecutorName:   "fake",
		Status:         StatusRunning,
		ExecutorHandle: `{"name":"x","namespace":"ns","gvr":"fake"}`,
		Ts:             start,
		StartedAt:      &start,
	}
	if err := db.Create(&inj).Error; err != nil {
		t.Fatalf("create inj: %v", err)
	}

	cw := newCaptureWebhook(t)
	exec := &statusFakeExecutor{statusState: ExecStatePending, statusDiag: map[string]any{}}
	// start(12:00) + 60s duration + 10m grace = 12:11:00; pick a clock past it.
	r := newStuckReconciler(db, exec, cw.sender(db), start.Add(20*time.Minute))
	r.reconcileOne(context.Background(), &inj)

	var got Injection
	if err := db.Where("id = ?", inj.ID).Take(&got).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.FinishedAt == nil {
		t.Fatalf("expected finished_at set")
	}
	if got.Diagnostics["reason"] != "cr_never_injected" {
		t.Fatalf("expected reason cr_never_injected, got %v", got.Diagnostics["reason"])
	}

	payload, hits := cw.snapshot()
	if hits != 1 {
		t.Fatalf("expected exactly one webhook delivery, got %d", hits)
	}
	if payload.Status != StatusFailed {
		t.Fatalf("webhook fired with status %q, want failed", payload.Status)
	}
	if payload.InjectionID != inj.ID {
		t.Fatalf("webhook injection_id %q, want %q", payload.InjectionID, inj.ID)
	}
}

// Guard: a fresh in-flight Pending CR (well before its expected completion)
// must NOT be force-failed — that would abort every normal injection during
// its selector/inject latency window.
func TestReconciler_FreshPendingNotForceFailed(t *testing.T) {
	db := newReconcilerTestDB(t)
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	inj := Injection{
		ID:             "inj-fresh",
		PointID:        "p1",
		Params:         JSONMap{"duration_s": float64(60)},
		IdempotencyKey: "idem-fresh",
		ExecutorName:   "fake",
		Status:         StatusRunning,
		ExecutorHandle: `{"name":"x","namespace":"ns","gvr":"fake"}`,
		Ts:             start,
		StartedAt:      &start,
	}
	if err := db.Create(&inj).Error; err != nil {
		t.Fatalf("create inj: %v", err)
	}

	cw := newCaptureWebhook(t)
	exec := &statusFakeExecutor{statusState: ExecStatePending, statusDiag: map[string]any{}}
	// Only 30s after start — inside duration+grace, so not stuck.
	r := newStuckReconciler(db, exec, cw.sender(db), start.Add(30*time.Second))
	r.reconcileOne(context.Background(), &inj)

	var got Injection
	if err := db.Where("id = ?", inj.ID).Take(&got).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("fresh pending row should stay running, got %s", got.Status)
	}
	if got.FinishedAt != nil {
		t.Fatalf("fresh pending row should not be finalized")
	}
	if _, hits := cw.snapshot(); hits != 0 {
		t.Fatalf("expected no webhook for fresh pending, got %d", hits)
	}
}

// Guard: CreateInjection persists the row as `pending` before Apply creates
// the CR. A reconciler tick landing in that in-request window reads the
// not-yet-applied CR as Orphaned (cr_absent). A fresh row in that window must
// NOT be force-failed as cr_vanished_mid_flight and must NOT fire a spurious
// failed webhook — that poisons the receiver's claim-once gate so the real
// `succeeded` webhook later no-ops and BuildDatapack never runs.
func TestReconciler_FreshOrphanedNotForceFailed(t *testing.T) {
	db := newReconcilerTestDB(t)
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	inj := Injection{
		ID:             "inj-fresh-orphan",
		PointID:        "p1",
		Params:         JSONMap{"duration_s": float64(60)},
		IdempotencyKey: "idem-fresh-orphan",
		ExecutorName:   "fake",
		Status:         StatusPending,
		ExecutorHandle: `{"name":"x","namespace":"ns","gvr":"fake"}`,
		Ts:             start,
	}
	if err := db.Create(&inj).Error; err != nil {
		t.Fatalf("create inj: %v", err)
	}

	cw := newCaptureWebhook(t)
	exec := &statusFakeExecutor{statusState: ExecStateOrphaned, statusDiag: map[string]any{"reason": "cr_absent"}}
	// 5s after create — inside the apply grace, so the CR is presumed not yet
	// Applied rather than vanished.
	r := newStuckReconciler(db, exec, cw.sender(db), start.Add(5*time.Second))
	r.reconcileOne(context.Background(), &inj)

	var got Injection
	if err := db.Where("id = ?", inj.ID).Take(&got).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != StatusPending {
		t.Fatalf("fresh orphaned row should stay pending, got %s", got.Status)
	}
	if got.FinishedAt != nil {
		t.Fatalf("fresh orphaned row should not be finalized")
	}
	if _, hits := cw.snapshot(); hits != 0 {
		t.Fatalf("expected no webhook for fresh orphaned, got %d", hits)
	}
}
