package chaoshooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/sirupsen/logrus"
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

func TestSingletonFailedReleasesNamespaceLock(t *testing.T) {
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&HookSubmission{}, &model.FaultInjection{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var releasedNs, releasedTrace string
	var calls int
	h := NewHandlerWithSubmitter(db, (&recorder{}).submit())
	h.releaseLock = func(_ context.Context, namespace, traceID string) error {
		calls++
		releasedNs, releasedTrace = namespace, traceID
		return nil
	}
	r := gin.New()
	r.POST("/api/v1/hooks/chaos", h.Singleton)

	meta := sampleMeta()
	meta.Namespace = "otel-demo0"
	body := SingletonWebhook{
		InjectionID:    "inj-fail-lock",
		IdempotencyKey: "k-fail-lock",
		Status:         statusFailed,
		CallerMetadata: meta,
	}
	w := postJSON(t, r, "/api/v1/hooks/chaos", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	if calls != 1 {
		t.Fatalf("expected exactly one lock release, got %d", calls)
	}
	if releasedNs != "otel-demo0" || releasedTrace != meta.TraceID {
		t.Fatalf("released wrong lock: ns=%q trace=%q", releasedNs, releasedTrace)
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

// TestOneShotChaosExtendsAbnormalWindow pins the one-shot timing fix: a
// PodKill-shaped webhook whose CR finished ~3s after start must have the
// downstream BuildDatapack's abnormal-window end pushed out to
// start+FixedAbnormalWindowSeconds, so the CH freshness probe waits for the
// post-fault observation window instead of firing on the collapsed window.
func TestOneShotChaosExtendsAbnormalWindow(t *testing.T) {
	_, rec, r := setupRig(t)
	start := time.Now().Add(-3 * time.Second).UTC()
	finished := start.Add(3 * time.Second) // CR completes near-instantly
	body := SingletonWebhook{
		InjectionID:    "inj-oneshot",
		IdempotencyKey: "k-oneshot",
		Status:         statusSucceeded,
		StartedAt:      start,
		FinishedAt:     finished,
		CallerMetadata: sampleMeta(),
	}
	postJSON(t, r, "/api/v1/hooks/chaos", body, nil)
	if rec.count() != 1 {
		t.Fatalf("want 1 submit got %d", rec.count())
	}
	dp := rec.tasks[0].task.Payload[consts.BuildDatapack].(dto.InjectionItem)
	want := start.Add(consts.FixedAbnormalWindowSeconds * time.Second)
	if !dp.EndTime.Equal(want) {
		t.Fatalf("one-shot EndTime: want %v (start+window) got %v", want, dp.EndTime)
	}
}

// TestDurationChaosWindowUnchanged proves the fix is a no-op for
// duration-based chaos: the CR ran the full FixedAbnormalWindow, so
// finishedAt already sits at the planned end and EndTime must not move.
func TestDurationChaosWindowUnchanged(t *testing.T) {
	_, rec, r := setupRig(t)
	start := time.Now().Add(-2 * consts.FixedAbnormalWindowSeconds * time.Second).UTC()
	finished := start.Add((consts.FixedAbnormalWindowSeconds + 5) * time.Second)
	body := SingletonWebhook{
		InjectionID:    "inj-duration",
		IdempotencyKey: "k-duration",
		Status:         statusSucceeded,
		StartedAt:      start,
		FinishedAt:     finished,
		CallerMetadata: sampleMeta(),
	}
	postJSON(t, r, "/api/v1/hooks/chaos", body, nil)
	if rec.count() != 1 {
		t.Fatalf("want 1 submit got %d", rec.count())
	}
	dp := rec.tasks[0].task.Payload[consts.BuildDatapack].(dto.InjectionItem)
	if !dp.EndTime.Equal(finished) {
		t.Fatalf("duration EndTime: want unchanged %v got %v", finished, dp.EndTime)
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

// TestShadowFIPersistsTaskIDWhenHasBackendTask locks in §11 step 5b
// cleanup #2: the chaos webhook writes fault_injections.task_id back to
// meta.TaskID when the caller flagged HasBackendTask=true, so audit /
// lineage queries can join the shadow row to its dispatching task.
func TestShadowFIPersistsTaskIDWhenHasBackendTask(t *testing.T) {
	db, _, _ := setupRig(t)
	h := NewHandlerWithSubmitter(db, func(context.Context, *dto.UnifiedTask) error { return nil })

	meta := sampleMeta()
	meta.TaskID = "task-bd"
	meta.HasBackendTask = true

	got, err := h.getOrCreateShadowFaultInjection(context.Background(), "chaos-real-1", &meta, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("create shadow: %v", err)
	}
	if got == nil || got.TaskID == nil || *got.TaskID != meta.TaskID {
		t.Fatalf("want TaskID=%q on shadow row, got %+v", meta.TaskID, got.TaskID)
	}
}

// TestShadowFINilTaskIDWhenAegisctlViaChaos locks in the inverse: when
// HasBackendTask=false (aegisctl --via-chaos client-side UUID), the
// shadow row leaves TaskID nil so the production FK to tasks(id) does
// not fire on the real MySQL schema.
func TestShadowFINilTaskIDWhenAegisctlViaChaos(t *testing.T) {
	db, _, _ := setupRig(t)
	h := NewHandlerWithSubmitter(db, func(context.Context, *dto.UnifiedTask) error { return nil })

	meta := sampleMeta()
	meta.TaskID = "no-such-task-uuid"
	meta.HasBackendTask = false

	got, err := h.getOrCreateShadowFaultInjection(context.Background(), "chaos-cli-1", &meta, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("create shadow: %v", err)
	}
	if got == nil {
		t.Fatalf("want shadow row, got nil")
	}
	if got.TaskID != nil {
		t.Fatalf("want TaskID=nil for aegisctl --via-chaos, got %q", *got.TaskID)
	}
}

// TestShadowFIRetriesOnFKViolation locks in the defensive retry: if the
// caller flagged HasBackendTask=true but the parent row vanished (race
// with a DELETE / GC), the first Create fails with an FK violation; the
// handler must retry once with TaskID=nil so the shadow row still
// lands, preserving the BuildDatapack downstream submission instead of
// losing the whole hook delivery.
//
// sqlite has no native FK enforcement in our test rig (other model
// tables aren't migrated, so seeding a real parent fails too), so the
// violation is simulated by a one-shot gorm BeforeCreate callback that
// errors on the first insert with TaskID set. This mirrors production:
// fault_injections.task_id FK fires → Create returns an error → handler
// retries with TaskID=nil.
type testLogHook struct {
	mu      sync.Mutex
	entries []logrus.Entry
}

func (h *testLogHook) Levels() []logrus.Level { return []logrus.Level{logrus.WarnLevel} }
func (h *testLogHook) Fire(e *logrus.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, *e)
	return nil
}

func TestShadowFIRetriesOnFKViolation(t *testing.T) {
	db, _, _ := setupRig(t)
	hook := &testLogHook{}
	logrus.AddHook(hook)
	t.Cleanup(func() { logrus.StandardLogger().ReplaceHooks(logrus.LevelHooks{}) })
	fired := false
	if err := db.Callback().Create().Before("gorm:create").Register("test:fk_violation_first", func(tx *gorm.DB) {
		if fired {
			return
		}
		fi, ok := tx.Statement.Dest.(*model.FaultInjection)
		if !ok || fi.TaskID == nil {
			return
		}
		fired = true
		tx.AddError(errors.New("FOREIGN KEY constraint failed (simulated)"))
	}); err != nil {
		t.Fatalf("register callback: %v", err)
	}

	h := NewHandlerWithSubmitter(db, func(context.Context, *dto.UnifiedTask) error { return nil })

	meta := sampleMeta()
	meta.TaskID = "ghost-task-uuid"
	meta.HasBackendTask = true

	got, err := h.getOrCreateShadowFaultInjection(context.Background(), "chaos-ghost", &meta, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("expected successful retry, got err: %v", err)
	}
	if !fired {
		t.Fatalf("callback never fired — the first Create did not include TaskID")
	}
	if got == nil {
		t.Fatalf("want shadow row after retry, got nil")
	}
	if got.TaskID != nil {
		t.Fatalf("want TaskID=nil after FK-retry, got %q", *got.TaskID)
	}
	if got.ChaosInjectionID != "chaos-ghost" {
		t.Fatalf("want ChaosInjectionID=chaos-ghost, got %q", got.ChaosInjectionID)
	}
	var warn *logrus.Entry
	for i := range hook.entries {
		if hook.entries[i].Message == "shadow FI retry without TaskID due to FK violation on backend dispatcher path" {
			warn = &hook.entries[i]
			break
		}
	}
	if warn == nil {
		t.Fatalf("want WARN log for shadow FI retry without TaskID, got entries: %+v", hook.entries)
	}
	if warn.Data["chaos_injection_id"] != "chaos-ghost" {
		t.Fatalf("want chaos_injection_id=chaos-ghost, got %v", warn.Data["chaos_injection_id"])
	}
	if warn.Data["task_id"] != "ghost-task-uuid" {
		t.Fatalf("want task_id=ghost-task-uuid, got %v", warn.Data["task_id"])
	}
}
