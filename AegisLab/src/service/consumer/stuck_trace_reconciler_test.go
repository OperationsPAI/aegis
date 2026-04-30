package consumer

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"aegis/consts"
	"aegis/dto"
	redis "aegis/infra/redis"
	"aegis/model"
	execution "aegis/module/execution"
	injection "aegis/module/injection"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// fakeInjectionOwner is the minimal seam reconciler tests need: it captures
// the state/timestamp updates the recovery path applies to a stuck trace.
type fakeInjectionOwner struct {
	mu                 sync.Mutex
	stateUpdates       []injection.RuntimeUpdateInjectionStateReq
	timestampUpdates   []injection.RuntimeUpdateInjectionTimestampReq
	timestampReturnErr error
	stateReturnErr     error
	// stored mirrors the FaultInjection rows we wrote so
	// UpdateInjectionTimestamps can return a populated InjectionItem.
	stored map[string]*model.FaultInjection
}

func newFakeInjectionOwner(rows []model.FaultInjection) *fakeInjectionOwner {
	stored := make(map[string]*model.FaultInjection, len(rows))
	for i := range rows {
		stored[rows[i].Name] = &rows[i]
	}
	return &fakeInjectionOwner{stored: stored}
}

func (f *fakeInjectionOwner) CreateInjection(_ context.Context, _ *injection.RuntimeCreateInjectionReq) (*dto.InjectionItem, error) {
	return nil, nil
}

func (f *fakeInjectionOwner) UpdateInjectionState(_ context.Context, req *injection.RuntimeUpdateInjectionStateReq) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stateUpdates = append(f.stateUpdates, *req)
	return f.stateReturnErr
}

func (f *fakeInjectionOwner) UpdateInjectionTimestamps(_ context.Context, req *injection.RuntimeUpdateInjectionTimestampReq) (*dto.InjectionItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.timestampUpdates = append(f.timestampUpdates, *req)
	if f.timestampReturnErr != nil {
		return nil, f.timestampReturnErr
	}
	row, ok := f.stored[req.Name]
	if !ok {
		row = &model.FaultInjection{ID: 1, Name: req.Name}
	}
	start := req.StartTime
	end := req.EndTime
	row.StartTime = &start
	row.EndTime = &end
	item := dto.NewInjectionItem(row)
	return &item, nil
}

type fakeExecutionOwner struct{}

func (fakeExecutionOwner) CreateExecution(context.Context, *execution.RuntimeCreateExecutionReq) (int, error) {
	return 0, nil
}
func (fakeExecutionOwner) GetExecution(context.Context, int) (*execution.ExecutionDetailResp, error) {
	return nil, nil
}
func (fakeExecutionOwner) UpdateExecutionState(context.Context, *execution.RuntimeUpdateExecutionStateReq) error {
	return nil
}

type fakeEnvVarLister struct {
	envVars []dto.ParameterItem
	err     error
}

func (f *fakeEnvVarLister) ListEnvVarsByVersionID(int) ([]dto.ParameterItem, error) {
	return f.envVars, f.err
}

func newReconcilerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Trace{},
		&model.Task{},
		&model.FaultInjection{},
	))
	return db
}

// reconcilerFixture builds a stuck trace + its FaultInjection task + a
// FaultInjection record at the given staleness.
type reconcilerFixture struct {
	traceID         string
	faultTaskID     string
	injectionName   string
	durationMinutes int
}

func makeStuckFixture(t *testing.T, db *gorm.DB, lastEvent consts.EventType, durationMin int, staleness time.Duration, hybrid bool) reconcilerFixture {
	t.Helper()
	now := time.Now()
	stuckAt := now.Add(-staleness)

	traceID := uuid.NewString()
	require.NoError(t, db.Create(&model.Trace{
		ID:        traceID,
		Type:      consts.TraceTypeFullPipeline,
		LastEvent: lastEvent,
		StartTime: stuckAt.Add(-1 * time.Minute),
		State:     consts.TraceRunning,
		Status:    consts.CommonEnabled,
		LeafNum:   1,
		UpdatedAt: stuckAt,
	}).Error)

	faultTaskID := uuid.NewString()
	guidedConfigs := []guidedcli.GuidedConfig{{
		System:    "ts",
		Namespace: "ts",
		ChaosType: "PodKill",
		Duration:  intPtr(durationMin),
	}}
	if hybrid {
		guidedConfigs = append(guidedConfigs, guidedcli.GuidedConfig{
			System:    "ts",
			Namespace: "ts",
			ChaosType: "JVMReturn",
			Duration:  intPtr(durationMin),
		})
	}
	payload := map[string]any{
		consts.InjectBenchmark: dto.ContainerVersionItem{
			ID: 7, ContainerName: "rcabench", Command: "/bin/echo",
		},
		consts.InjectPreDuration:   float64(1),
		consts.InjectGuidedConfigs: guidedConfigs,
		consts.InjectNamespace:     "ts",
		consts.InjectPedestal:      "ts",
		consts.InjectPedestalID:    float64(11),
		consts.InjectSystem:        "ts",
	}
	pj, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Task{
		ID:        faultTaskID,
		Type:      consts.TaskTypeFaultInjection,
		TraceID:   traceID,
		Payload:   string(pj),
		State:     consts.TaskRunning,
		Status:    consts.CommonEnabled,
		Level:     1,
		Sequence:  0,
		UpdatedAt: stuckAt,
	}).Error)

	// Set CreatedAt explicitly: it's the immutable "fault start" anchor
	// the reconciler now uses for both the duration gate and the
	// synthesized abnormal window. UpdatedAt is left as auto-now to
	// reflect production semantics — GORM bumps it on every save (e.g.
	// UpdateInjectionState), so a test that pinned UpdatedAt to stuckAt
	// would mask the very bug we're guarding against.
	injectionName := "fi-" + uuid.NewString()
	require.NoError(t, db.Create(&model.FaultInjection{
		Name:      injectionName,
		Category:  "ts",
		TaskID:    &faultTaskID,
		State:     consts.DatapackInitial,
		Status:    consts.CommonEnabled,
		CreatedAt: stuckAt,
	}).Error)
	if hybrid {
		require.NoError(t, db.Create(&model.FaultInjection{
			Name:      injectionName + "-leaf2",
			Category:  "ts",
			TaskID:    &faultTaskID,
			State:     consts.DatapackInitial,
			Status:    consts.CommonEnabled,
			CreatedAt: stuckAt.Add(time.Second),
		}).Error)
	}

	return reconcilerFixture{
		traceID:         traceID,
		faultTaskID:     faultTaskID,
		injectionName:   injectionName,
		durationMinutes: durationMin,
	}
}

func intPtr(v int) *int { return &v }

func newReconcilerForTest(t *testing.T, db *gorm.DB, owner *fakeInjectionOwner, submit taskSubmitter) *StuckTraceReconciler {
	t.Helper()
	r := &StuckTraceReconciler{
		db:                    db,
		store:                 newStateStore(fakeExecutionOwner{}, owner),
		containerRepo:         &fakeEnvVarLister{},
		now:                   time.Now,
		intervalSeconds:       func() int { return 60 },
		stuckThresholdSeconds: func() int { return 60 },
		submitTask:            submit,
		maxBatchPerTick:       50,
	}
	return r
}

// TestReconciler_RecoversTraceStuckAtFaultInjectionCompleted exercises the
// in-memory batchManager-loss path described in the issue #293 timeline:
// fault.injection.completed was the trace's last_event but no BuildDatapack
// task ever materialised. The reconciler must submit one.
func TestReconciler_RecoversTraceStuckAtFaultInjectionCompleted(t *testing.T) {
	db := newReconcilerTestDB(t)
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionCompleted, 1, 30*time.Minute, false)

	owner := newFakeInjectionOwner([]model.FaultInjection{
		{ID: 1, Name: fix.injectionName, TaskID: &fix.faultTaskID, PreDuration: 1},
	})

	var captured []*dto.UnifiedTask
	submitter := taskSubmitter(func(_ context.Context, _ *gorm.DB, _ *redis.Gateway, t *dto.UnifiedTask) error {
		captured = append(captured, t)
		return nil
	})

	r := newReconcilerForTest(t, db, owner, submitter)
	processed, _, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, captured, 1)

	got := captured[0]
	require.Equal(t, consts.TaskTypeBuildDatapack, got.Type)
	require.True(t, got.Immediate)
	require.Equal(t, fix.traceID, got.TraceID)
	require.NotNil(t, got.ParentTaskID)
	require.Equal(t, fix.faultTaskID, *got.ParentTaskID)

	// state + timestamps must have been written.
	require.Equal(t, []injection.RuntimeUpdateInjectionStateReq{{
		Name:  fix.injectionName,
		State: consts.DatapackInjectSuccess,
	}}, owner.stateUpdates)
	require.Len(t, owner.timestampUpdates, 1)
	require.Equal(t, fix.injectionName, owner.timestampUpdates[0].Name)
}

// TestReconciler_IsIdempotent simulates the CRD-success path racing the
// reconciler: if a BuildDatapack child task already exists for the same
// FaultInjection task, the reconciler must NOT submit a second one.
func TestReconciler_IsIdempotent(t *testing.T) {
	db := newReconcilerTestDB(t)
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionCompleted, 1, 30*time.Minute, false)

	// The CRD-success path got there first.
	require.NoError(t, db.Create(&model.Task{
		ID:           uuid.NewString(),
		Type:         consts.TaskTypeBuildDatapack,
		TraceID:      fix.traceID,
		ParentTaskID: &fix.faultTaskID,
		Payload:      "{}",
		State:        consts.TaskPending,
		Status:       consts.CommonEnabled,
	}).Error)

	owner := newFakeInjectionOwner([]model.FaultInjection{
		{ID: 1, Name: fix.injectionName, TaskID: &fix.faultTaskID},
	})
	called := 0
	submitter := taskSubmitter(func(_ context.Context, _ *gorm.DB, _ *redis.Gateway, _ *dto.UnifiedTask) error {
		called++
		return nil
	})
	r := newReconcilerForTest(t, db, owner, submitter)
	processed, _, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed)
	require.Equal(t, 0, called)
}

// TestReconciler_RespectsStuckThreshold guards against the reconciler
// stealing in-flight traces that are still inside their fault window.
// updated_at within the stuck threshold must be left alone.
func TestReconciler_RespectsStuckThreshold(t *testing.T) {
	db := newReconcilerTestDB(t)
	// Fresh trace, only 10s old. Stuck threshold default is 600s.
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionCompleted, 1, 10*time.Second, false)
	_ = fix

	owner := newFakeInjectionOwner(nil)
	submitter := taskSubmitter(func(context.Context, *gorm.DB, *redis.Gateway, *dto.UnifiedTask) error {
		t.Fatal("submit must not be called")
		return nil
	})
	r := newReconcilerForTest(t, db, owner, submitter)
	r.stuckThresholdSeconds = func() int { return 600 }
	processed, _, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed)
}

// TestReconciler_TickReportsCandidateCount pins the observability contract
// that closes issue #305. Before this change, tick returned only `processed`
// — when stuck candidates existed but every one was skipped (threshold not
// yet hit, idempotency win, etc.) the reconciler logged nothing and was
// indistinguishable from a goroutine that never started. The heartbeat in
// Run() depends on tick reporting the candidate count truthfully.
func TestReconciler_TickReportsCandidateCount(t *testing.T) {
	db := newReconcilerTestDB(t)
	// Fresh trace inside the threshold window: it is a candidate per the
	// SQL filter (state=Running, last_event matches, status active) only
	// AFTER the threshold has elapsed. With a 10-second age and a 1-second
	// threshold, the SELECT picks it up; the per-row threshold check then
	// keeps it (CreatedAt + duration is far in the future).
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionCompleted, 60, 10*time.Second, false)
	_ = fix

	owner := newFakeInjectionOwner([]model.FaultInjection{
		{ID: 1, Name: fix.injectionName, TaskID: &fix.faultTaskID, PreDuration: 1},
	})
	submitter := taskSubmitter(func(context.Context, *gorm.DB, *redis.Gateway, *dto.UnifiedTask) error {
		t.Fatal("submit must not be called for fresh in-window trace")
		return nil
	})
	r := newReconcilerForTest(t, db, owner, submitter)
	r.stuckThresholdSeconds = func() int { return 1 }
	processed, candidates, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed, "in-window trace must not be processed")
	require.Equal(t, 1, candidates, "candidate count must report the SELECT row count, not just submits — silent reconciler regression guard for #305")
}

// TestReconciler_HybridBatchRecoversWithoutBatchManager covers the
// in-memory batchManager-race path: K_inner=2 with both leaves DB-resident
// must still complete via the reconciler when the in-memory counter is
// gone (worker restart or race-lost increment).
func TestReconciler_HybridBatchRecoversWithoutBatchManager(t *testing.T) {
	db := newReconcilerTestDB(t)
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionCompleted, 1, 30*time.Minute, true)

	owner := newFakeInjectionOwner([]model.FaultInjection{
		{ID: 1, Name: fix.injectionName, TaskID: &fix.faultTaskID},
		{ID: 2, Name: fix.injectionName + "-leaf2", TaskID: &fix.faultTaskID},
	})

	var captured []*dto.UnifiedTask
	submitter := taskSubmitter(func(_ context.Context, _ *gorm.DB, _ *redis.Gateway, t *dto.UnifiedTask) error {
		captured = append(captured, t)
		return nil
	})
	r := newReconcilerForTest(t, db, owner, submitter)
	processed, _, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed, "hybrid batch must recover with exactly one BuildDatapack submit")
	require.Len(t, captured, 1)
}

// TestReconciler_StuckAtFaultInjectionStartedRespectsDuration covers the
// round-3 path: trace stuck at fault.injection.started because the worker
// died before CRD-success ever fired. We must not finalize until
// updated_at + max(guided.Duration) + grace is in the past.
func TestReconciler_StuckAtFaultInjectionStartedRespectsDuration(t *testing.T) {
	db := newReconcilerTestDB(t)
	// Trace stuck 3min ago, but guided duration is 5min — fault might
	// still be running (no CRD-success means we trust duration).
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionStarted, 5, 3*time.Minute, false)
	_ = fix

	owner := newFakeInjectionOwner([]model.FaultInjection{})
	submitter := taskSubmitter(func(context.Context, *gorm.DB, *redis.Gateway, *dto.UnifiedTask) error {
		t.Fatal("submit must not be called: fault still inside duration window")
		return nil
	})
	r := newReconcilerForTest(t, db, owner, submitter)
	r.stuckThresholdSeconds = func() int { return 60 } // pull trace into scan
	processed, _, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed)
}

// TestReconciler_StuckAtFaultInjectionStartedFinalizesAfterDuration is the
// other half: once updated_at + duration + grace is in the past we
// finalize the trace and submit BuildDatapack.
func TestReconciler_StuckAtFaultInjectionStartedFinalizesAfterDuration(t *testing.T) {
	db := newReconcilerTestDB(t)
	// Trace stuck 30min ago, guided duration 1min — well past the
	// duration + grace window.
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionStarted, 1, 30*time.Minute, false)

	owner := newFakeInjectionOwner([]model.FaultInjection{
		{ID: 1, Name: fix.injectionName, TaskID: &fix.faultTaskID},
	})
	var captured []*dto.UnifiedTask
	submitter := taskSubmitter(func(_ context.Context, _ *gorm.DB, _ *redis.Gateway, t *dto.UnifiedTask) error {
		captured = append(captured, t)
		return nil
	})
	r := newReconcilerForTest(t, db, owner, submitter)
	r.stuckThresholdSeconds = func() int { return 60 }
	processed, _, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, captured, 1)

	// Parent FaultInjection task must be advanced to TaskCompleted, otherwise
	// selectBestLastEvent leaves trace.last_event pinned at fault.injection.started
	// even after the recovered BuildDatapack chain finishes downstream. This
	// is the behavior the second round of Copilot review on PR #294 caught:
	// submitting BuildDatapack alone without advancing the parent task wedges
	// the trace status at the originally-stuck event forever.
	var parent model.Task
	require.NoError(t, db.Where("id = ?", fix.faultTaskID).First(&parent).Error)
	require.Equal(t, consts.TaskCompleted, parent.State, "FaultInjection parent must be advanced to TaskCompleted")
}

// TestReconciler_StuckAtFaultInjectionCompletedDoesNotReadvanceParent
// verifies the parent-advance step is gated to last_event=Started — for
// traces already past Completed, the parent is already TaskCompleted, so
// re-touching it would be a wasted DB write at best and a regression
// signal at worst (we'd be implying we re-derive Completed traces, which
// we don't).
func TestReconciler_StuckAtFaultInjectionCompletedDoesNotReadvanceParent(t *testing.T) {
	db := newReconcilerTestDB(t)
	// 30min staleness vs 5min guided duration so we're past the duration gate.
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionCompleted, 5, 30*time.Minute, false)

	// Inflate a non-current updated_at on the parent task so we can detect
	// whether the reconciler wrote to it. If it doesn't write, updated_at
	// stays at this old value (within 1ms tolerance).
	frozen := time.Now().Add(-15 * time.Minute)
	require.NoError(t, db.Model(&model.Task{}).
		Where("id = ?", fix.faultTaskID).
		UpdateColumn("updated_at", frozen).Error)

	owner := newFakeInjectionOwner([]model.FaultInjection{
		{ID: 1, Name: fix.injectionName, TaskID: &fix.faultTaskID},
	})
	submitter := taskSubmitter(func(_ context.Context, _ *gorm.DB, _ *redis.Gateway, _ *dto.UnifiedTask) error {
		return nil
	})
	r := newReconcilerForTest(t, db, owner, submitter)
	r.stuckThresholdSeconds = func() int { return 60 }
	processed, _, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	var parent model.Task
	require.NoError(t, db.Where("id = ?", fix.faultTaskID).First(&parent).Error)
	require.WithinDuration(t, frozen, parent.UpdatedAt, time.Second,
		"parent task updated_at should not have moved — completed-stuck path must not re-touch the parent")
}

// TestReconciler_ToleratesStateUpdateError verifies the reconciler does not
// fail closed when the injection state update fails — postProcess in
// k8s_handler.go uses errCtx.Warn for this exact case, so we must mirror.
func TestReconciler_ToleratesStateUpdateError(t *testing.T) {
	db := newReconcilerTestDB(t)
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionCompleted, 1, 30*time.Minute, false)

	owner := newFakeInjectionOwner([]model.FaultInjection{
		{ID: 1, Name: fix.injectionName, TaskID: &fix.faultTaskID},
	})
	owner.stateReturnErr = context.DeadlineExceeded // any error

	var captured []*dto.UnifiedTask
	submitter := taskSubmitter(func(_ context.Context, _ *gorm.DB, _ *redis.Gateway, t *dto.UnifiedTask) error {
		captured = append(captured, t)
		return nil
	})
	r := newReconcilerForTest(t, db, owner, submitter)
	processed, _, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed, "state-update warning must not block BuildDatapack submission")
	require.Len(t, captured, 1)
}

// TestMaxGuidedDurationMinutes_PicksLargest pins the helper that controls
// the "is this fault still running?" gate: K_inner>=2 batches with mixed
// durations must use the longest one.
func TestMaxGuidedDurationMinutes_PicksLargest(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    int
	}{
		{
			name:    "missing guided_configs falls back to 5",
			payload: map[string]any{},
			want:    5,
		},
		{
			name: "single duration",
			payload: map[string]any{
				consts.InjectGuidedConfigs: []guidedcli.GuidedConfig{{Duration: intPtr(3)}},
			},
			want: 3,
		},
		{
			name: "max of multiple",
			payload: map[string]any{
				consts.InjectGuidedConfigs: []guidedcli.GuidedConfig{
					{Duration: intPtr(2)},
					{Duration: intPtr(7)},
					{Duration: intPtr(4)},
				},
			},
			want: 7,
		},
		{
			name: "all-nil falls back to 5",
			payload: map[string]any{
				consts.InjectGuidedConfigs: []guidedcli.GuidedConfig{{}, {}},
			},
			want: 5,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := maxGuidedDurationMinutes(tc.payload)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestReconciler_SynthesizesAbnormalWindowForward pins the regression for
// Copilot's time-math comment on stuck_trace_reconciler.go. When a
// FaultInjection row is missing both StartTime and EndTime, the reconciler
// must derive [CreatedAt, CreatedAt + duration]. CreatedAt is the immutable
// row-INSERT timestamp written when the chaos CRD is created; the fault
// then runs forward for `duration` minutes. UpdatedAt would be wrong here
// because GORM bumps it on every save (e.g. UpdateInjectionState a few
// lines earlier), so anchoring synthesis to UpdatedAt would shift the
// window forward by however long the unrelated state-write took.
func TestReconciler_SynthesizesAbnormalWindowForward(t *testing.T) {
	db := newReconcilerTestDB(t)
	const durationMin = 5
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionCompleted, durationMin, 30*time.Minute, false)

	// Mirror the fixture's CreatedAt exactly so the assertion reads the
	// synthesized window without timing fuzz.
	var row model.FaultInjection
	require.NoError(t, db.Where("name = ?", fix.injectionName).First(&row).Error)
	require.Nil(t, row.StartTime, "fixture must NOT set StartTime; this test exercises the synthesis path")
	require.Nil(t, row.EndTime, "fixture must NOT set EndTime; this test exercises the synthesis path")
	createdAt := row.CreatedAt

	owner := newFakeInjectionOwner([]model.FaultInjection{
		{ID: row.ID, Name: fix.injectionName, TaskID: &fix.faultTaskID, CreatedAt: createdAt},
	})

	submitter := taskSubmitter(func(_ context.Context, _ *gorm.DB, _ *redis.Gateway, _ *dto.UnifiedTask) error {
		return nil
	})
	r := newReconcilerForTest(t, db, owner, submitter)
	processed, _, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	require.Len(t, owner.timestampUpdates, 1)
	got := owner.timestampUpdates[0]
	wantStart := createdAt
	wantEnd := createdAt.Add(durationMin * time.Minute)
	require.True(t, got.StartTime.Equal(wantStart),
		"start_time must equal CreatedAt (%s), got %s", wantStart, got.StartTime)
	require.True(t, got.EndTime.Equal(wantEnd),
		"end_time must equal CreatedAt + duration (%s), got %s", wantEnd, got.EndTime)
	require.True(t, got.EndTime.After(got.StartTime),
		"window must run forward from CreatedAt, not backward")
}

// TestReconciler_PrefersStoredTimestampsOverSynthesis verifies that stored
// StartTime/EndTime on the FaultInjection row override the synthesis path.
// This is the post-CRD-success case: the per-leaf updateInjectionTimestamp
// landed in the DB but BuildDatapack never got submitted.
func TestReconciler_PrefersStoredTimestampsOverSynthesis(t *testing.T) {
	db := newReconcilerTestDB(t)
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionCompleted, 5, 30*time.Minute, false)

	storedStart := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	storedEnd := storedStart.Add(7 * time.Minute)
	require.NoError(t, db.Model(&model.FaultInjection{}).
		Where("name = ?", fix.injectionName).
		Updates(map[string]any{"start_time": storedStart, "end_time": storedEnd}).Error)

	owner := newFakeInjectionOwner([]model.FaultInjection{
		{ID: 1, Name: fix.injectionName, TaskID: &fix.faultTaskID, StartTime: &storedStart, EndTime: &storedEnd},
	})
	submitter := taskSubmitter(func(_ context.Context, _ *gorm.DB, _ *redis.Gateway, _ *dto.UnifiedTask) error {
		return nil
	})
	r := newReconcilerForTest(t, db, owner, submitter)
	processed, _, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	require.Len(t, owner.timestampUpdates, 1)
	got := owner.timestampUpdates[0]
	require.True(t, got.StartTime.Equal(storedStart), "stored StartTime must win over synthesis")
	require.True(t, got.EndTime.Equal(storedEnd), "stored EndTime must win over synthesis")
}

// TestReconciler_ConcurrentTicksSubmitOnce simulates two reconciler replicas
// running tick() concurrently against the same stuck trace. The transaction
// + recheck must guarantee exactly one BuildDatapack lands. Without the
// FOR UPDATE / re-check coupling this races and submits twice.
func TestReconciler_ConcurrentTicksSubmitOnce(t *testing.T) {
	// Use a shared in-memory DSN so both goroutines and the orchestrator
	// see the same SQLite database. The default ":memory:" form gives
	// each connection its own isolated DB, which would let the race
	// "succeed" trivially even under buggy code.
	db, err := gorm.Open(
		sqlite.Open("file:reconciler_race?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Trace{}, &model.Task{}, &model.FaultInjection{}))
	t.Cleanup(func() {
		sql, err := db.DB()
		if err == nil {
			_ = sql.Close()
		}
	})

	fix := makeStuckFixture(t, db, consts.EventFaultInjectionCompleted, 1, 30*time.Minute, false)
	rowSnapshot := model.FaultInjection{ID: 1, Name: fix.injectionName, TaskID: &fix.faultTaskID}

	// The submitter persists a child Task row inside the same tx the
	// reconciler hands it. This is what the production submitter (
	// common.SubmitTaskWithDB) does, and it's what the in-tx idempotency
	// recheck on the second goroutine needs to observe.
	persistChild := func(_ context.Context, tx *gorm.DB, _ *redis.Gateway, task *dto.UnifiedTask) error {
		row := &model.Task{
			ID:           uuid.NewString(),
			Type:         task.Type,
			TraceID:      task.TraceID,
			ParentTaskID: task.ParentTaskID,
			Payload:      "{}",
			State:        consts.TaskPending,
			Status:       consts.CommonEnabled,
		}
		return tx.Create(row).Error
	}

	tickOnce := func() (int, error) {
		owner := newFakeInjectionOwner([]model.FaultInjection{rowSnapshot})
		r := newReconcilerForTest(t, db, owner, persistChild)
		processed, _, err := r.tick(context.Background())
		return processed, err
	}

	var wg sync.WaitGroup
	results := make([]int, 2)
	errs := make([]error, 2)
	wg.Add(2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = tickOnce()
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d", i)
	}
	require.Equal(t, 1, results[0]+results[1],
		"exactly one of the racing reconcilers must observe a successful submit; got %v", results)

	// Belt-and-braces: the DB itself must hold exactly one BuildDatapack
	// child row.
	var n int64
	require.NoError(t, db.Model(&model.Task{}).
		Where("parent_task_id = ? AND type = ?", fix.faultTaskID, consts.TaskTypeBuildDatapack).
		Count(&n).Error)
	require.Equal(t, int64(1), n, "exactly one BuildDatapack child must exist after concurrent ticks")
}

// TestReconciler_ResolveIntervalRespectsConfig pins the ticker plumbing
// (Copilot comment on Run): the reconciler must read interval-from-config
// on every cycle so an etcd push at runtime is honored without a worker
// restart. Resolution is unit-tested directly; the live ticker.Reset is
// exercised by integration suites — running a real ticker in unit tests
// is racy and adds no signal.
func TestReconciler_ResolveIntervalRespectsConfig(t *testing.T) {
	db := newReconcilerTestDB(t)
	r := newReconcilerForTest(t, db, newFakeInjectionOwner(nil), nil)

	configured := 7
	r.intervalSeconds = func() int { return configured }
	require.Equal(t, 7*time.Second, r.resolveInterval())

	configured = 30
	require.Equal(t, 30*time.Second, r.resolveInterval(),
		"resolveInterval must re-read the config getter, not cache")

	configured = 0
	require.Equal(t,
		time.Duration(consts.DefaultStuckTraceReconcileIntervalSecs)*time.Second,
		r.resolveInterval(),
		"non-positive config must fall through to the default")
}

// TestReconciler_GatesAndSynthesisIgnoreUpdatedAtBumps reproduces the
// production failure mode the CreatedAt switch is closing.
//
// Setup: a FaultInjection row born well past the fault window
// (CreatedAt = now - 30min, guidedDuration = 5min, so the window
// [CreatedAt, CreatedAt + 5min] ended ~25min ago). Then an unrelated
// control-plane write — modeled here by a GORM Save that flips State —
// auto-bumps UpdatedAt to ~now via autoUpdateTime. This is exactly what
// UpdateInjectionState does in production once the chaos-mesh
// informer fires.
//
// Pre-fix behavior: the duration gate read inj.UpdatedAt (~now), so
// threshold = now + 5min + grace, the gate said "still in window",
// and the trace was kept stuck across ticks even though the fault
// completed half an hour ago. Recovery would never fire until the
// trace fell out of the scan window.
//
// Post-fix behavior: the gate reads inj.CreatedAt (now - 30min), so
// threshold = now - 23min, the gate correctly says "past window", and
// the reconciler proceeds. The synthesis path then anchors start/end
// to CreatedAt, not UpdatedAt — so BuildDatapack queries the correct
// abnormal window even though UpdatedAt is far in the future relative
// to the actual fault.
func TestReconciler_GatesAndSynthesisIgnoreUpdatedAtBumps(t *testing.T) {
	db := newReconcilerTestDB(t)
	const durationMin = 5
	const stalenessMin = 30
	fix := makeStuckFixture(t, db, consts.EventFaultInjectionCompleted, durationMin, stalenessMin*time.Minute, false)

	// Simulate the unrelated state write that happens in production
	// (UpdateInjectionState et al.). Use a real GORM Save so
	// autoUpdateTime bumps UpdatedAt to ~now — exactly what bites the
	// reconciler. CreatedAt stays at the fixture's stuckAt.
	var row model.FaultInjection
	require.NoError(t, db.Where("name = ?", fix.injectionName).First(&row).Error)
	originalCreatedAt := row.CreatedAt
	row.State = consts.DatapackInjectSuccess
	require.NoError(t, db.Save(&row).Error)

	var afterBump model.FaultInjection
	require.NoError(t, db.Where("name = ?", fix.injectionName).First(&afterBump).Error)
	require.True(t, afterBump.CreatedAt.Equal(originalCreatedAt),
		"GORM Save must preserve CreatedAt; got %s want %s",
		afterBump.CreatedAt, originalCreatedAt)
	require.True(t, afterBump.UpdatedAt.After(originalCreatedAt.Add(time.Duration(stalenessMin-1)*time.Minute)),
		"GORM autoUpdateTime must have bumped UpdatedAt to ~now (>= %d min after CreatedAt); got UpdatedAt=%s CreatedAt=%s",
		stalenessMin-1, afterBump.UpdatedAt, originalCreatedAt)

	owner := newFakeInjectionOwner([]model.FaultInjection{afterBump})
	var captured []*dto.UnifiedTask
	submitter := taskSubmitter(func(_ context.Context, _ *gorm.DB, _ *redis.Gateway, t *dto.UnifiedTask) error {
		captured = append(captured, t)
		return nil
	})

	r := newReconcilerForTest(t, db, owner, submitter)
	processed, _, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed,
		"duration gate anchored to CreatedAt must let the trace finalize: "+
			"row was created %dmin ago and the %dmin fault window has long since elapsed",
		stalenessMin, durationMin)
	require.Len(t, captured, 1)

	// Synthesis must use CreatedAt, not the auto-bumped UpdatedAt.
	require.Len(t, owner.timestampUpdates, 1)
	got := owner.timestampUpdates[0]
	require.True(t, got.StartTime.Equal(originalCreatedAt),
		"synthesized start_time must equal CreatedAt (%s), got %s — "+
			"if this asserts UpdatedAt the synthesis is reading the "+
			"auto-bumped field and BuildDatapack will query the wrong window",
		originalCreatedAt, got.StartTime)
	require.True(t, got.EndTime.Equal(originalCreatedAt.Add(durationMin*time.Minute)),
		"synthesized end_time must equal CreatedAt + duration; got %s",
		got.EndTime)
}
