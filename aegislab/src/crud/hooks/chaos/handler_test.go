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
	"aegis/platform/model"
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
	if err := db.AutoMigrate(&HookSubmission{}, &model.FaultInjection{}); err != nil {
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

func TestSingletonCancelledNoFire(t *testing.T) {
	_, rec, r := setupRig(t)
	body := SingletonWebhook{
		InjectionID:    "inj-c",
		IdempotencyKey: "kc",
		Status:         statusCancelled,
		CallerMetadata: sampleMeta(),
	}
	postJSON(t, r, "/api/v1/hooks/chaos", body, nil)
	if got := rec.count(); got != 0 {
		t.Fatalf("cancelled: want 0 got %d", got)
	}
	postJSON(t, r, "/api/v1/hooks/chaos", body, nil)
	if got := rec.count(); got != 0 {
		t.Fatalf("cancelled replay: want 0 got %d", got)
	}
}

func TestBatchFailedNoFire(t *testing.T) {
	_, rec, r := setupRig(t)
	body := BatchWebhook{
		BatchID:             "batch-f",
		IdempotencyKey:      "bkf",
		AggregatedStatus:    statusFailed,
		BatchCallerMetadata: sampleMeta(),
	}
	postJSON(t, r, "/api/v1/hooks/chaos-batch", body, nil)
	if got := rec.count(); got != 0 {
		t.Fatalf("failed: want 0 got %d", got)
	}
	postJSON(t, r, "/api/v1/hooks/chaos-batch", body, nil)
	if got := rec.count(); got != 0 {
		t.Fatalf("failed replay: want 0 got %d", got)
	}
}

func TestRootTraceCarrierPropagated(t *testing.T) {
	_, rec, r := setupRig(t)
	meta := sampleMeta()
	meta.RootTraceCarrier = propagation.MapCarrier{"traceparent": "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"}
	body := SingletonWebhook{
		InjectionID:    "inj-root",
		IdempotencyKey: "kroot",
		Status:         statusSucceeded,
		CallerMetadata: meta,
	}
	postJSON(t, r, "/api/v1/hooks/chaos", body, nil)
	if rec.count() != 1 {
		t.Fatalf("want 1 submit")
	}
	got := rec.tasks[0].task.RootTraceCarrier
	if got["traceparent"] != meta.RootTraceCarrier["traceparent"] {
		t.Fatalf("RootTraceCarrier not propagated: %+v", got)
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

// TestBatchSucceededThenPartialIsNoOp pins the design §11 step 4 prereq:
// downstream submission is idempotent on (injection_id, task_type). The
// gate's PK is (id, kind), so once a terminal claims the gate any later
// terminal (including a different one) for the same (id, kind) is a
// no-op — the receiver MUST NOT fire a second BuildDatapack.
func TestBatchSucceededThenPartialIsNoOp(t *testing.T) {
	_, rec, r := setupRig(t)
	first := BatchWebhook{
		BatchID:             "batch-dual",
		IdempotencyKey:      "bk-dual-1",
		AggregatedStatus:    statusSucceeded,
		BatchCallerMetadata: sampleMeta(),
	}
	postJSON(t, r, "/api/v1/hooks/chaos-batch", first, nil)
	if got := rec.count(); got != 1 {
		t.Fatalf("first (succeeded): want 1 submit got %d", got)
	}

	second := first
	second.IdempotencyKey = "bk-dual-2"
	second.AggregatedStatus = statusPartial
	postJSON(t, r, "/api/v1/hooks/chaos-batch", second, nil)
	if got := rec.count(); got != 1 {
		t.Fatalf("second (partial) for same batch_id: want still 1 submit got %d", got)
	}
}

func TestGetOrCreateShadowFaultInjectionIdempotent(t *testing.T) {
	db, _, _ := setupRig(t)
	h := NewHandlerWithSubmitter(db, func(context.Context, *dto.UnifiedTask) error { return nil })

	meta := sampleMeta()
	meta.Pedestal = "otel-demo"
	meta.PreDuration = 60
	start := time.Now().Add(-2 * time.Minute).UTC()
	end := time.Now().UTC()

	first, err := h.getOrCreateShadowFaultInjection(context.Background(), "chaos-inj-1", &meta, start, end)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first == nil || first.ID == 0 {
		t.Fatalf("first call: want non-zero FI, got %+v", first)
	}

	second, err := h.getOrCreateShadowFaultInjection(context.Background(), "chaos-inj-1", &meta, start, end)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("idempotency broken: first.ID=%d second.ID=%d", first.ID, second.ID)
	}

	var n int64
	if err := db.Model(&model.FaultInjection{}).Where("chaos_injection_id = ?", "chaos-inj-1").Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 row, got %d", n)
	}
}

func TestGetOrCreateShadowFaultInjectionEmptyChaosIDNoOp(t *testing.T) {
	db, _, _ := setupRig(t)
	h := NewHandlerWithSubmitter(db, func(context.Context, *dto.UnifiedTask) error { return nil })

	meta := sampleMeta()
	got, err := h.getOrCreateShadowFaultInjection(context.Background(), "", &meta, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil row for empty chaos_injection_id, got %+v", got)
	}
	var n int64
	if err := db.Model(&model.FaultInjection{}).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("want no rows, got %d", n)
	}
}

func TestSingletonCreatesShadowFaultInjection(t *testing.T) {
	db, rec, r := setupRig(t)
	meta := sampleMeta()
	meta.Pedestal = "otel-demo"
	meta.PreDuration = 90
	body := SingletonWebhook{
		InjectionID:    "01HZZZULIDFOR-SHADOW",
		IdempotencyKey: "k-shadow",
		Status:         statusSucceeded,
		StartedAt:      time.Now().Add(-time.Minute).UTC(),
		FinishedAt:     time.Now().UTC(),
		CallerMetadata: meta,
	}

	w := postJSON(t, r, "/api/v1/hooks/chaos", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}

	var rows []model.FaultInjection
	if err := db.Where("chaos_injection_id = ?", body.InjectionID).Find(&rows).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 shadow FI row, got %d", len(rows))
	}
	got := rows[0]
	if got.Name != meta.Datapack.Name {
		t.Fatalf("name mismatch: want %q got %q", meta.Datapack.Name, got.Name)
	}
	if got.PreDuration != meta.Datapack.PreDuration {
		t.Fatalf("pre_duration mismatch: want %d got %d", meta.Datapack.PreDuration, got.PreDuration)
	}
	if got.StartTime == nil || !got.StartTime.Equal(body.StartedAt) {
		t.Fatalf("start_time mismatch: want %v got %v", body.StartedAt, got.StartTime)
	}
	if got.EndTime == nil || !got.EndTime.Equal(body.FinishedAt) {
		t.Fatalf("end_time mismatch: want %v got %v", body.FinishedAt, got.EndTime)
	}
	if got.State != consts.DatapackInjectSuccess {
		t.Fatalf("state want DatapackInjectSuccess got %v", got.State)
	}

	if rec.count() != 1 {
		t.Fatalf("want 1 build-datapack task, got %d", rec.count())
	}
	task := rec.tasks[0].task
	dpPayload, ok := task.Payload[consts.BuildDatapack].(dto.InjectionItem)
	if !ok {
		t.Fatalf("payload[BuildDatapack] missing/wrong type: %T", task.Payload[consts.BuildDatapack])
	}
	if dpPayload.ID != got.ID {
		t.Fatalf("downstream datapack.ID want shadow %d got %d", got.ID, dpPayload.ID)
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
