package chaoshooks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/testutil"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

func init() {
	// Default global propagator is a no-op; tests pump traceparent through
	// it explicitly.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	gin.SetMode(gin.TestMode)
}

type submittedTask struct {
	task *dto.UnifiedTask
	ctx  context.Context
}

type recorder struct {
	mu    sync.Mutex
	tasks []submittedTask
}

func (r *recorder) submit() TaskSubmitter {
	return func(ctx context.Context, t *dto.UnifiedTask) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.tasks = append(r.tasks, submittedTask{task: t, ctx: ctx})
		return nil
	}
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.tasks)
}

func setupRig(t *testing.T) (*gorm.DB, *recorder, *gin.Engine) {
	t.Helper()
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&HookSubmission{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rec := &recorder{}
	h := NewHandlerWithSubmitter(db, rec.submit())
	r := gin.New()
	r.POST("/api/v1/hooks/chaos", h.Singleton)
	r.POST("/api/v1/hooks/chaos-batch", h.Batch)
	return db, rec, r
}

func sampleMeta() CallerMetadata {
	return CallerMetadata{
		TaskID:    "task-A",
		TaskType:  "FaultInjection",
		TraceID:   "trace-A",
		GroupID:   "group-A",
		ProjectID: 7,
		UserID:    11,
		Benchmark: &dto.ContainerVersionItem{ID: 42, Name: "ts"},
		Datapack:  &dto.InjectionItem{ID: 101, Name: "inj-101", PreDuration: 60},
	}
}

func postJSON(t *testing.T, r *gin.Engine, path string, body any, hdr http.Header) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSingletonSucceededFiresOnce(t *testing.T) {
	_, rec, r := setupRig(t)
	body := SingletonWebhook{
		InjectionID:    "inj-1",
		IdempotencyKey: "k1",
		Status:         statusSucceeded,
		StartedAt:      time.Now().Add(-time.Minute),
		FinishedAt:     time.Now(),
		CallerMetadata: sampleMeta(),
	}

	w := postJSON(t, r, "/api/v1/hooks/chaos", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("first call: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	if got := rec.count(); got != 1 {
		t.Fatalf("first call: want 1 submission got %d", got)
	}

	// Replay: same id + same status → no second submission.
	w2 := postJSON(t, r, "/api/v1/hooks/chaos", body, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("replay: want 200 got %d", w2.Code)
	}
	if got := rec.count(); got != 1 {
		t.Fatalf("replay: want 1 submission got %d", got)
	}

	// Verify the downstream task carried the right ParentTaskID + payload.
	sub := rec.tasks[0]
	if sub.task.Type != consts.TaskTypeBuildDatapack {
		t.Fatalf("want BuildDatapack got %v", sub.task.Type)
	}
	if sub.task.ParentTaskID == nil || *sub.task.ParentTaskID != "task-A" {
		t.Fatalf("ParentTaskID mismatch: %+v", sub.task.ParentTaskID)
	}
	if sub.task.TraceID != "trace-A" {
		t.Fatalf("TraceID mismatch: %s", sub.task.TraceID)
	}
}

func TestSingletonFailedDoesNotFire(t *testing.T) {
	_, rec, r := setupRig(t)
	body := SingletonWebhook{
		InjectionID:    "inj-2",
		IdempotencyKey: "k2",
		Status:         statusFailed,
		CallerMetadata: sampleMeta(),
	}
	w := postJSON(t, r, "/api/v1/hooks/chaos", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	if got := rec.count(); got != 0 {
		t.Fatalf("want 0 submissions got %d", got)
	}
}

func TestBatchSucceededFiresOnce(t *testing.T) {
	_, rec, r := setupRig(t)
	body := BatchWebhook{
		BatchID:             "batch-1",
		IdempotencyKey:      "bk1",
		AggregatedStatus:    statusSucceeded,
		BatchCallerMetadata: sampleMeta(),
		ChildResults: []ChildResult{
			{InjectionID: "c1", Status: statusSucceeded},
			{InjectionID: "c2", Status: statusSucceeded},
		},
	}
	w := postJSON(t, r, "/api/v1/hooks/chaos-batch", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	if got := rec.count(); got != 1 {
		t.Fatalf("want 1 got %d", got)
	}

	// Replay → still one.
	postJSON(t, r, "/api/v1/hooks/chaos-batch", body, nil)
	if got := rec.count(); got != 1 {
		t.Fatalf("replay: want 1 got %d", got)
	}
}

func TestBatchPartialFires(t *testing.T) {
	_, rec, r := setupRig(t)
	body := BatchWebhook{
		BatchID:             "batch-p",
		IdempotencyKey:      "bkp",
		AggregatedStatus:    statusPartial,
		BatchCallerMetadata: sampleMeta(),
	}
	postJSON(t, r, "/api/v1/hooks/chaos-batch", body, nil)
	if got := rec.count(); got != 1 {
		t.Fatalf("partial: want 1 got %d", got)
	}
}

func TestBatchCancelledNoFire(t *testing.T) {
	_, rec, r := setupRig(t)
	body := BatchWebhook{
		BatchID:             "batch-c",
		IdempotencyKey:      "bkc",
		AggregatedStatus:    statusCancelled,
		BatchCallerMetadata: sampleMeta(),
	}
	postJSON(t, r, "/api/v1/hooks/chaos-batch", body, nil)
	if got := rec.count(); got != 0 {
		t.Fatalf("cancelled: want 0 got %d", got)
	}
	// Replay still 0.
	postJSON(t, r, "/api/v1/hooks/chaos-batch", body, nil)
	if got := rec.count(); got != 0 {
		t.Fatalf("cancelled replay: want 0 got %d", got)
	}
}

func TestW3CTraceparentBecomesParent(t *testing.T) {
	_, rec, r := setupRig(t)
	traceID := "11111111111111111111111111111111"
	spanID := "2222222222222222"
	traceparent := "00-" + traceID + "-" + spanID + "-01"

	body := SingletonWebhook{
		InjectionID:    "inj-tp",
		IdempotencyKey: "ktp",
		Status:         statusSucceeded,
		CallerMetadata: sampleMeta(),
	}
	hdr := http.Header{}
	hdr.Set("traceparent", traceparent)

	w := postJSON(t, r, "/api/v1/hooks/chaos", body, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	if rec.count() != 1 {
		t.Fatalf("want 1 submit")
	}

	got := oteltrace.SpanContextFromContext(rec.tasks[0].ctx)
	if !got.IsValid() {
		t.Fatalf("downstream ctx has no span: %+v", got)
	}
	if got.TraceID().String() != traceID {
		t.Fatalf("traceID not propagated: want %s got %s", traceID, got.TraceID().String())
	}
}
