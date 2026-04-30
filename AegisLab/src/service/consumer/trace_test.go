package consumer

import (
	"context"
	"testing"
	"time"

	"aegis/consts"
	"aegis/dto"
	"aegis/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTraceStateTestDB builds an in-memory SQLite DB with the Trace + Task
// schema. Mirrors newReconcilerTestDB / newK8sHandlerTestDB so test setup
// stays consistent across consumer tests.
func newTraceStateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Trace{}, &model.Task{}))
	return db
}

// TestTryUpdateTraceStateCore_BDRescheduleAdvancesLastEvent is the issue
// #312 regression guard. Setup mirrors the byte-cluster K=12 ts campaign
// Round 12 corruption: a FullPipeline trace whose level-1 FaultInjection
// completed and whose level-2 BuildDatapack child has just been moved to
// TaskRescheduled (the rate-limit-token-timeout / freshness-deferred path
// in rescheduleBuildDatapackTask). Pre-fix, inferTraceState counted the
// rescheduled task as Pending, fell through to Priority 4, and re-asserted
// EventFaultInjectionCompleted as the trace's last_event — leaving the
// trace permanently stuck while BD retried in the background. Post-fix,
// the explicit streamEvent.EventName from rescheduleBuildDatapackTask
// surfaces to traces.last_event.
func TestTryUpdateTraceStateCore_BDRescheduleAdvancesLastEvent(t *testing.T) {
	db := newTraceStateTestDB(t)

	traceID := uuid.NewString()
	faultTaskID := uuid.NewString()
	bdTaskID := uuid.NewString()
	now := time.Now()

	require.NoError(t, db.Create(&model.Trace{
		ID:        traceID,
		Type:      consts.TraceTypeFullPipeline,
		LastEvent: consts.EventFaultInjectionCompleted,
		State:     consts.TraceRunning,
		Status:    consts.CommonEnabled,
		LeafNum:   1,
		StartTime: now.Add(-10 * time.Minute),
		UpdatedAt: now.Add(-5 * time.Minute),
	}).Error)
	require.NoError(t, db.Create(&model.Task{
		ID:       faultTaskID,
		Type:     consts.TaskTypeFaultInjection,
		TraceID:  traceID,
		Payload:  "{}",
		State:    consts.TaskCompleted,
		Status:   consts.CommonEnabled,
		Level:    1,
		Sequence: 0,
	}).Error)
	require.NoError(t, db.Create(&model.Task{
		ID:       bdTaskID,
		Type:     consts.TaskTypeBuildDatapack,
		TraceID:  traceID,
		Payload:  "{}",
		State:    consts.TaskRescheduled,
		Status:   consts.CommonEnabled,
		Level:    2,
		Sequence: 0,
	}).Error)

	streamEvent := &dto.TraceStreamEvent{
		TaskID:    bdTaskID,
		TaskType:  consts.TaskTypeBuildDatapack,
		EventName: consts.EventNoTokenAvailable,
	}

	err := tryUpdateTraceStateCore(nil, context.Background(), db, traceID, bdTaskID,
		consts.TaskRescheduled, streamEvent)
	require.NoError(t, err)

	var got model.Trace
	require.NoError(t, db.First(&got, "id = ?", traceID).Error)
	require.Equal(t, consts.EventNoTokenAvailable, got.LastEvent,
		"BD reschedule must advance trace.last_event off fault.injection.completed")
	require.Equal(t, consts.TraceRunning, got.State,
		"reschedule must not flip the trace to a terminal state on its own")
}

// TestTryUpdateTraceStateCore_BDFailureAdvancesLastEvent guards the
// k8s-job-failure path (HandleJobFailed) for the same trace shape.
// The job-failed path emits EventDatapackBuildFailed and moves the BD
// task to TaskError; inferTraceState's Priority 1 (all-failed at this
// level) should already produce EventDatapackBuildFailed via
// selectBestLastEvent's TaskError fallback. This test pins that
// behavior so a future refactor of the reschedule branch above doesn't
// silently regress the genuine-job-failure case.
func TestTryUpdateTraceStateCore_BDFailureAdvancesLastEvent(t *testing.T) {
	db := newTraceStateTestDB(t)

	traceID := uuid.NewString()
	faultTaskID := uuid.NewString()
	bdTaskID := uuid.NewString()
	now := time.Now()

	require.NoError(t, db.Create(&model.Trace{
		ID:        traceID,
		Type:      consts.TraceTypeFullPipeline,
		LastEvent: consts.EventFaultInjectionCompleted,
		State:     consts.TraceRunning,
		Status:    consts.CommonEnabled,
		LeafNum:   1,
		StartTime: now.Add(-10 * time.Minute),
		UpdatedAt: now.Add(-5 * time.Minute),
	}).Error)
	require.NoError(t, db.Create(&model.Task{
		ID:       faultTaskID,
		Type:     consts.TaskTypeFaultInjection,
		TraceID:  traceID,
		Payload:  "{}",
		State:    consts.TaskCompleted,
		Status:   consts.CommonEnabled,
		Level:    1,
		Sequence: 0,
	}).Error)
	require.NoError(t, db.Create(&model.Task{
		ID:       bdTaskID,
		Type:     consts.TaskTypeBuildDatapack,
		TraceID:  traceID,
		Payload:  "{}",
		State:    consts.TaskError,
		Status:   consts.CommonEnabled,
		Level:    2,
		Sequence: 0,
	}).Error)

	streamEvent := &dto.TraceStreamEvent{
		TaskID:    bdTaskID,
		TaskType:  consts.TaskTypeBuildDatapack,
		EventName: consts.EventDatapackBuildFailed,
	}

	err := tryUpdateTraceStateCore(nil, context.Background(), db, traceID, bdTaskID,
		consts.TaskError, streamEvent)
	require.NoError(t, err)

	var got model.Trace
	require.NoError(t, db.First(&got, "id = ?", traceID).Error)
	require.Equal(t, consts.EventDatapackBuildFailed, got.LastEvent)
}

// TestTryUpdateTraceStateCore_StaleRescheduleDoesNotOverwriteTerminal pins
// the race-guard added per Copilot review on PR #313. Setup: BD task has
// already advanced to TaskCompleted in the DB (a later updateTraceState
// goroutine wrote the terminal state), but a stale TaskRescheduled call is
// arriving late on the same channel with newState=TaskRescheduled and a
// reschedule streamEvent. Without the guard, the override would overwrite
// the trace's terminal last_event with no.token.available — strictly
// worse than the original #312 corruption. With the guard, the persisted
// task state (TaskCompleted) wins and inferTraceState's normal path
// produces the correct terminal event.
func TestTryUpdateTraceStateCore_StaleRescheduleDoesNotOverwriteTerminal(t *testing.T) {
	db := newTraceStateTestDB(t)

	traceID := uuid.NewString()
	faultTaskID := uuid.NewString()
	bdTaskID := uuid.NewString()
	now := time.Now()

	require.NoError(t, db.Create(&model.Trace{
		ID:        traceID,
		Type:      consts.TraceTypeFullPipeline,
		LastEvent: consts.EventFaultInjectionCompleted,
		State:     consts.TraceRunning,
		Status:    consts.CommonEnabled,
		LeafNum:   1,
		StartTime: now.Add(-15 * time.Minute),
		UpdatedAt: now.Add(-5 * time.Minute),
	}).Error)
	require.NoError(t, db.Create(&model.Task{
		ID:       faultTaskID,
		Type:     consts.TaskTypeFaultInjection,
		TraceID:  traceID,
		Payload:  "{}",
		State:    consts.TaskCompleted,
		Status:   consts.CommonEnabled,
		Level:    1,
		Sequence: 0,
	}).Error)
	// BD persisted as TaskCompleted — the later goroutine has already won.
	require.NoError(t, db.Create(&model.Task{
		ID:       bdTaskID,
		Type:     consts.TaskTypeBuildDatapack,
		TraceID:  traceID,
		Payload:  "{}",
		State:    consts.TaskCompleted,
		Status:   consts.CommonEnabled,
		Level:    2,
		Sequence: 0,
	}).Error)

	staleStreamEvent := &dto.TraceStreamEvent{
		TaskID:    bdTaskID,
		TaskType:  consts.TaskTypeBuildDatapack,
		EventName: consts.EventNoTokenAvailable,
	}

	// Late-arriving stale reschedule call — newState input still says
	// TaskRescheduled because that was the value at goroutine spawn.
	err := tryUpdateTraceStateCore(nil, context.Background(), db, traceID, bdTaskID,
		consts.TaskRescheduled, staleStreamEvent)
	require.NoError(t, err)

	var got model.Trace
	require.NoError(t, db.First(&got, "id = ?", traceID).Error)
	require.NotEqual(t, consts.EventNoTokenAvailable, got.LastEvent,
		"stale reschedule event must not overwrite a trace whose BD task already persisted as TaskCompleted")
}
