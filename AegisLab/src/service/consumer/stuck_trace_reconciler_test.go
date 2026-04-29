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

	injectionName := "fi-" + uuid.NewString()
	require.NoError(t, db.Create(&model.FaultInjection{
		Name:      injectionName,
		Category:  "ts",
		TaskID:    &faultTaskID,
		State:     consts.DatapackInitial,
		Status:    consts.CommonEnabled,
		UpdatedAt: stuckAt,
	}).Error)
	if hybrid {
		require.NoError(t, db.Create(&model.FaultInjection{
			Name:      injectionName + "-leaf2",
			Category:  "ts",
			TaskID:    &faultTaskID,
			State:     consts.DatapackInitial,
			Status:    consts.CommonEnabled,
			UpdatedAt: stuckAt.Add(time.Second),
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
	processed, err := r.tick(context.Background())
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
	processed, err := r.tick(context.Background())
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
	processed, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed)
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
	processed, err := r.tick(context.Background())
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
	processed, err := r.tick(context.Background())
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
	processed, err := r.tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, captured, 1)
}

// TestReconciler_TolersatesStateUpdateError verifies the reconciler does not
// fail closed when the injection state update fails — postProcess in
// k8s_handler.go uses errCtx.Warn for this exact case, so we must mirror.
func TestReconciler_TolersatesStateUpdateError(t *testing.T) {
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
	processed, err := r.tick(context.Background())
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
