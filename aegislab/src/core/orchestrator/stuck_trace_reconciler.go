package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/dto"
	redis "aegis/platform/redis"
	"aegis/platform/model"
	"aegis/platform/tracing"
	container "aegis/core/domain/container"
	"aegis/core/orchestrator/common"
	"aegis/platform/utils"

	guidedcli "aegis/platform/chaos"
)

// StuckTraceReconciler is a DB-driven sweep that recovers traces stuck at
// last_event in {fault.injection.started, fault.injection.completed} with no
// BuildDatapack child task. Closes issue #293: the CRD-success path is the
// only thing that submits BuildDatapack, so a worker restart between
// CRD-success and SubmitTaskWithDB, an in-memory batchManager race, or a
// silently early-returning postProcess closure leaves the trace pinned and
// the campaign loses the datapack.
//
// Idempotency: a child BuildDatapack task already linked to the
// FaultInjection task is treated as "already fired" and skipped, so multiple
// replicas can run the reconciler safely without distributed locks.
type StuckTraceReconciler struct {
	db                    *gorm.DB
	redisGateway          *redis.Gateway
	store                 *stateStore
	containerRepo         containerEnvVarLister
	now                   func() time.Time
	intervalSeconds       func() int
	stuckThresholdSeconds func() int
	submitTask            taskSubmitter
	maxBatchPerTick       int
	tickHook              func(processed int, err error)
	// namespaceLockReleaser releases the monitor:ns:<ns> hash held by a
	// stuck RestartPedestal task. Optional — when nil the lock release is
	// skipped (the lock TTL eventually expires anyway). Production wires
	// the orchestrator NamespaceMonitor here.
	namespaceLockReleaser namespaceLockReleaser
	// restartTokenReleaser / warmingTokenReleaser release the two
	// rate-limit tokens held by a stuck RestartPedestal task. Optional —
	// release is no-op on missing tokens, so calling defensively is safe.
	restartTokenReleaser tokenReleaser
	warmingTokenReleaser tokenReleaser
	// runOnce guards Run from a misconfigured fx wiring that calls
	// StartStuckTraceReconciler twice on the same instance. Instance-
	// scoped (not package-scoped) so a fresh reconciler from a new fx
	// lifecycle can start the loop again — important for tests and any
	// future in-process controller restart.
	runOnce sync.Once
}

// taskSubmitter is the seam tests use to capture the recovered BuildDatapack
// task without standing up the producer-side Redis machinery. The production
// implementation is common.SubmitTaskWithDB.
type taskSubmitter func(ctx context.Context, db *gorm.DB, redisGateway *redis.Gateway, task *dto.UnifiedTask) error

// container.Repository is needed for the same env-var merge postProcess does;
// re-stated as a tiny seam so we don't have to spin up the real repo in tests.
type containerEnvVarLister interface {
	ListEnvVarsByVersionID(versionID int) ([]dto.ParameterItem, error)
}

// namespaceLockReleaser is the subset of NamespaceMonitor the
// restart-pedestal recovery path touches. Pulled out so tests can supply a
// fake without standing up Redis.
type namespaceLockReleaser interface {
	ReleaseLock(ctx context.Context, namespace string, traceID string) error
}

// tokenReleaser is the subset of TokenBucketRateLimiter the
// restart-pedestal recovery path touches.
type tokenReleaser interface {
	ReleaseToken(ctx context.Context, taskID, traceID string) error
}

// NewStuckTraceReconciler builds the production reconciler. The constructor
// is also used by the controller module's fx wiring.
func NewStuckTraceReconciler(
	db *gorm.DB,
	redisGateway *redis.Gateway,
	executionOwner ExecutionOwner,
	injectionOwner InjectionOwner,
	monitor namespaceLockReleaser,
	restartLimiter tokenReleaser,
	warmingLimiter tokenReleaser,
) *StuckTraceReconciler {
	return &StuckTraceReconciler{
		db:                    db,
		redisGateway:          redisGateway,
		store:                 newStateStore(executionOwner, injectionOwner),
		containerRepo:         container.NewRepository(db),
		now:                   time.Now,
		intervalSeconds:       intervalSecondsFromConfig,
		stuckThresholdSeconds: stuckThresholdSecondsFromConfig,
		submitTask:            common.SubmitTaskWithDB,
		maxBatchPerTick:       50,
		namespaceLockReleaser: monitor,
		restartTokenReleaser:  restartLimiter,
		warmingTokenReleaser:  warmingLimiter,
	}
}

// Run drives the reconciler ticker. It exits when ctx is cancelled. The
// initial sleep is a full interval so a fresh worker doesn't immediately
// stomp the just-arrived CRD-success path; the bug we're closing only
// matters for traces that have been stuck longer than the threshold anyway.
//
// Interval changes pushed via etcd at runtime are picked up at the next tick
// by re-reading r.intervalSeconds() and calling ticker.Reset when the value
// has changed — no worker restart required.
//
// A startup INFO log + per-tick heartbeat (issue #305) makes a silent
// reconciler immediately visible in worker logs — previously the only
// signal was "tick recovered N" which logs only when N>0, so a quiet
// reconciler was indistinguishable from a never-launched goroutine.
func (r *StuckTraceReconciler) Run(ctx context.Context) {
	if r == nil || r.db == nil {
		logrus.Warn("StuckTraceReconciler.Run skipped: missing db")
		return
	}
	currentInterval := r.resolveInterval()
	stuckSecs := r.stuckThresholdSeconds()
	if stuckSecs <= 0 {
		stuckSecs = consts.DefaultStuckTraceReconcileStuckSecs
	}
	logrus.WithFields(logrus.Fields{
		"interval_seconds":        int(currentInterval / time.Second),
		"stuck_threshold_seconds": stuckSecs,
		"max_batch_per_tick":      r.maxBatchPerTick,
	}).Info("StuckTraceReconciler started")
	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logrus.Info("StuckTraceReconciler stopping: context cancelled")
			return
		case <-ticker.C:
		}
		processed, candidates, err := r.runTickSafely(ctx)
		fields := logrus.Fields{
			"candidates": candidates,
			"processed":  processed,
		}
		switch {
		case err != nil:
			logrus.WithFields(fields).WithError(err).Warn("stuck trace reconcile tick failed")
		case processed > 0:
			logrus.WithFields(fields).Infof("stuck trace reconcile tick recovered %d trace(s)", processed)
		case candidates > 0:
			// Candidates existed but none recovered (all skipped by
			// threshold/idempotency check). Surface as INFO so the
			// "reconciler quiet but stuck candidates exist" failure
			// mode (issue #305) is immediately visible.
			logrus.WithFields(fields).Info("stuck trace reconcile tick: no recoveries")
		default:
			logrus.WithFields(fields).Debug("stuck trace reconcile tick: no candidates")
		}
		if r.tickHook != nil {
			r.tickHook(processed, err)
		}
		if next := r.resolveInterval(); next != currentInterval {
			ticker.Reset(next)
			currentInterval = next
		}
	}
}

// runTickSafely wraps tick() so a panic during recovery doesn't kill the
// reconciler goroutine for the rest of the worker's lifetime (issue #305:
// a silent reconciler is a worse failure than a noisy one).
//
// The tick body runs under a "reconciler.tick" span on the
// aegis/observability tracer. This is a process-level observability
// span — it intentionally lives outside any user-facing trace tree.
func (r *StuckTraceReconciler) runTickSafely(ctx context.Context) (processed, candidates int, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			logrus.WithField("panic", rec).Error("StuckTraceReconciler.tick panicked, continuing loop")
			err = fmt.Errorf("tick panic: %v", rec)
		}
	}()
	tickCtx, span := otel.Tracer("aegis/observability").Start(ctx, "reconciler.tick",
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
	)
	defer span.End()
	processed, candidates, err = r.tick(tickCtx)
	span.SetAttributes(
		attribute.Int("reconciler.candidates", candidates),
		attribute.Int("reconciler.processed", processed),
	)
	return
}

func (r *StuckTraceReconciler) resolveInterval() time.Duration {
	v := time.Duration(r.intervalSeconds()) * time.Second
	if v <= 0 {
		v = time.Duration(consts.DefaultStuckTraceReconcileIntervalSecs) * time.Second
	}
	return v
}

// tick runs one reconcile sweep and returns (processed, candidates, err).
// processed = traces for which a BuildDatapack was actually submitted;
// candidates = stuck-trace rows the SELECT returned. Reporting both lets
// the heartbeat distinguish "no rows match" from "rows match but were
// skipped" — without that, a silent reconciler with stuck rows in the DB
// looks identical to a working one with nothing to do (issue #305).
func (r *StuckTraceReconciler) tick(ctx context.Context) (int, int, error) {
	stuckSecs := r.stuckThresholdSeconds()
	if stuckSecs <= 0 {
		stuckSecs = consts.DefaultStuckTraceReconcileStuckSecs
	}
	cutoff := r.now().Add(-time.Duration(stuckSecs) * time.Second)

	// restart.pedestal.started gets a longer threshold: a cold DSB bootstrap
	// legitimately parks here for readiness_timeout + warmup + traffic-gate
	// (tens of minutes) before the inject fires, without advancing updated_at.
	// Reaping it at the generic 600 s threshold kills in-progress bootstraps.
	rpSecs := restartPedestalStuckThresholdSeconds()
	if rpSecs < stuckSecs {
		rpSecs = stuckSecs
	}
	rpCutoff := r.now().Add(-time.Duration(rpSecs) * time.Second)

	// Anti-join out traces that already have a non-deleted BuildDatapack
	// task. Without this filter the candidate set is starved on busy
	// clusters: any trace whose last_event was never advanced past
	// fault.injection.* (e.g. BD ran and failed but the trace-state-update
	// silently dropped — see follow-up to #305) keeps reappearing in the
	// oldest-first scan, fills the LIMIT 50 budget every tick, and the
	// per-row idempotency check inside recoverTrace returns (false, nil)
	// for all of them. Newer genuinely-stuck traces (no BD child) are
	// ordered by updated_at ASC after the broken-state old rows and never
	// surface. The recoverTrace idempotency check stays as a defensive
	// race-condition guard but should never fire in practice once this
	// filter is in place.
	var traces []model.Trace
	err := r.db.WithContext(ctx).
		Model(&model.Trace{}).
		Where("state = ? AND status != ? AND ("+
			"(last_event IN ? AND updated_at < ?) OR "+
			"(last_event = ? AND updated_at < ?))",
			consts.TraceRunning,
			consts.CommonDeleted,
			[]consts.EventType{
				consts.EventFaultInjectionStarted,
				consts.EventFaultInjectionCompleted,
			},
			cutoff,
			consts.EventRestartPedestalStarted,
			rpCutoff,
		).
		Where("NOT EXISTS (?)",
			r.db.Model(&model.Task{}).
				Select("1").
				Where("tasks.trace_id = traces.id AND tasks.type = ? AND tasks.status != ?",
					consts.TaskTypeBuildDatapack,
					consts.CommonDeleted,
				),
		).
		Order("traces.updated_at ASC").
		Order("id ASC").
		Limit(r.maxBatchPerTick).
		Find(&traces).Error
	if err != nil {
		return 0, 0, fmt.Errorf("query stuck traces: %w", err)
	}

	processed := 0
	for i := range traces {
		recovered, err := r.recoverTrace(ctx, &traces[i])
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"trace_id":   traces[i].ID,
				"last_event": traces[i].LastEvent,
			}).WithError(err).Warn("failed to recover stuck trace")
			continue
		}
		if recovered {
			processed++
		}
	}
	return processed, len(traces), nil
}

// recoverTrace handles a single stuck trace. Returns (true, nil) iff a
// BuildDatapack task was actually submitted. (false, nil) means the trace
// was already advancing on its own (idempotency guard), so the next tick
// will skip it cleanly.
func (r *StuckTraceReconciler) recoverTrace(ctx context.Context, trace *model.Trace) (bool, error) {
	logEntry := logrus.WithFields(logrus.Fields{
		"trace_id":   trace.ID,
		"last_event": trace.LastEvent,
	})

	// Traces stuck at restart.pedestal.started never reached the
	// fault-injection step — there is nothing to forward to BuildDatapack.
	// Recovery is a clean cancel: mark the trace failed, release the
	// namespace lock + rate-limit tokens the dead worker is still holding,
	// let the user re-run the regression.
	if trace.LastEvent == consts.EventRestartPedestalStarted {
		return r.recoverStuckRestartPedestal(ctx, trace, logEntry)
	}

	// Find the FaultInjection task for this trace. Single-leaf and hybrid
	// batches both have exactly one FaultInjection task per trace; the
	// hybrid path differentiates only in how postProcess fans in (via
	// batchManager) and which name (chaos CRD vs. batch_id) it passes
	// into updateInjectionState. The DB-driven recovery does not need that
	// distinction — we look up the FaultInjection record(s) by TaskID.
	var fiTask model.Task
	err := r.db.WithContext(ctx).
		Where("trace_id = ? AND type = ? AND status != ?",
			trace.ID,
			consts.TaskTypeFaultInjection,
			consts.CommonDeleted,
		).
		First(&fiTask).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, fmt.Errorf("lookup fault-injection task: %w", err)
	}

	// Cheap fast-path idempotency check: if a BuildDatapack child task
	// already exists, we don't need to bother re-deriving payloads or
	// updating timestamps. The authoritative check still runs inside the
	// per-parent-row transaction in submitIfNoChild — this is just an
	// early exit to skip the work in the common already-advanced case.
	var existingDatapackCount int64
	if err := r.db.WithContext(ctx).
		Model(&model.Task{}).
		Where("parent_task_id = ? AND type = ? AND status != ?",
			fiTask.ID,
			consts.TaskTypeBuildDatapack,
			consts.CommonDeleted,
		).
		Count(&existingDatapackCount).Error; err != nil {
		return false, fmt.Errorf("idempotency check: %w", err)
	}
	if existingDatapackCount > 0 {
		logEntry.Debug("BuildDatapack task already exists for fault-injection task, skipping")
		return false, nil
	}

	// Find the FaultInjection record(s). For hybrid K_inner>=2 batches
	// there are multiple records sharing the same TaskID; we recover the
	// trace iff every leaf is past the inject window (StartTime/EndTime
	// non-nil OR the record's UpdatedAt is older than the stuck window —
	// the latter handles the case where the CRD-success closure failed
	// before updateInjectionTimestamp).
	var injections []model.FaultInjection
	if err := r.db.WithContext(ctx).
		Where("task_id = ? AND status != ?", fiTask.ID, consts.CommonDeleted).
		Find(&injections).Error; err != nil {
		return false, fmt.Errorf("lookup fault-injection records: %w", err)
	}
	if len(injections) == 0 {
		// FaultInjection record never made it to the DB — the inject
		// itself failed before CreateInjection ran. Not our problem;
		// upstream error handling owns this case.
		logEntry.Debug("no FaultInjection records for stuck trace, skipping")
		return false, nil
	}

	// Reconstruct the BuildDatapack payload from the FaultInjection task's
	// own payload — that's the only place the benchmark ContainerVersionItem
	// is preserved verbatim from the original submit. We don't need (and
	// won't try) to reconstruct the chaos-mesh CRD state.
	taskPayload, err := decodeTaskPayload(&fiTask)
	if err != nil {
		return false, fmt.Errorf("decode fault-injection payload: %w", err)
	}
	benchmark, err := utils.ConvertToType[dto.ContainerVersionItem](taskPayload[consts.InjectBenchmark])
	if err != nil {
		return false, fmt.Errorf("missing benchmark in fault-injection payload: %w", err)
	}
	namespace, _ := taskPayload[consts.InjectNamespace].(string)

	// Verify every leaf is "done enough". If a guided config has a
	// duration and the FaultInjection record's UpdatedAt + duration is
	// still in the future, the fault is genuinely still running — leave
	// it for a later tick. We do NOT query chaos-mesh here: under the
	// failure modes we're closing (informer event drop, worker restart,
	// silent postProcess early-return), chaos-mesh would tell us either
	// "Phase=Stop" (fault done, GC pending) or "not found" (already GC'd)
	// — both equivalent to "go ahead and finalize". The DB-side
	// updated_at + duration check is a sufficient proxy that doesn't
	// require us to wire a dynamic chaos-mesh client into this layer.
	guidedDuration := maxGuidedDurationMinutes(taskPayload)
	now := r.now()
	for i := range injections {
		inj := &injections[i]
		// If timestamps are populated, trust them: end_time + buffer is
		// the natural "done enough" marker.
		if inj.EndTime != nil {
			if now.Before(inj.EndTime.Add(stuckGraceWindow)) {
				logEntry.WithField("inj_name", inj.Name).
					Debug("FaultInjection.EndTime in future + grace, skipping")
				return false, nil
			}
			continue
		}
		// No timestamps: fall back to (CreatedAt + max guided duration +
		// grace). This covers the round-3 byte-cluster case where the
		// worker died between CRD-add and CRD-success and updateInjectionTimestamp
		// was never called. CreatedAt is the immutable INSERT timestamp
		// — UpdatedAt is auto-bumped by GORM on any subsequent write
		// (UpdateInjectionState, etc.) and would shift the window.
		threshold := inj.CreatedAt.Add(time.Duration(guidedDuration)*time.Minute + stuckGraceWindow)
		if now.Before(threshold) {
			logEntry.WithField("inj_name", inj.Name).
				Debug("FaultInjection.CreatedAt + duration still in future, skipping")
			return false, nil
		}
	}

	// Choose the injection record we'll attach to the BuildDatapack
	// payload. For single-leaf this is the only record; for hybrid we
	// pick the most-recently-updated row (the one with the freshest
	// CRD-success timestamps if the per-leaf updateInjectionTimestamp
	// landed at all).
	chosen := pickInjectionForDatapack(injections)

	// Update injection state + timestamps the same way postProcess does.
	// updateInjectionTimestamp is what produces the *dto.InjectionItem
	// fed into the BuildDatapack payload.
	if err := r.store.updateInjectionState(ctx, chosen.Name, consts.DatapackInjectSuccess); err != nil {
		logEntry.WithError(err).Warn("update injection state failed (continuing)")
	}

	// CreatedAt is the row INSERT timestamp, written exactly once when
	// the chaos CRD is created — that's "fault start" within seconds.
	// UpdatedAt would be tempting (it's set to the same value at INSERT
	// on a fresh row) but GORM auto-bumps it on every save, including
	// the UpdateInjectionState call a few lines up, so by the time we
	// hit synthesis here UpdatedAt no longer corresponds to fault start.
	// The fault runs forward from CreatedAt for `guidedDuration` minutes,
	// so the abnormal window is [CreatedAt, CreatedAt + duration].
	// Stored StartTime/EndTime override both when present.
	startTime := chosen.CreatedAt
	endTime := chosen.CreatedAt.Add(time.Duration(guidedDuration) * time.Minute)
	if chosen.StartTime != nil {
		startTime = *chosen.StartTime
	}
	if chosen.EndTime != nil {
		endTime = *chosen.EndTime
	}
	updatedItem, err := r.store.updateInjectionTimestamp(ctx, chosen.Name, startTime, endTime)
	if err != nil {
		// Non-fatal — even if the persist failed, BuildDatapack still
		// needs a valid time window or it will query CH for an empty
		// range and produce an empty datapack. dto.NewInjectionItem
		// would copy chosen.StartTime/EndTime, which are nil in the
		// stuck-trace fallback case. Build the item directly from the
		// already-computed [startTime, endTime] (anchored to CreatedAt
		// or to the stored timestamps if they were present).
		logEntry.WithError(err).Warn("update injection timestamps failed, falling back to computed window")
		updatedItem = &dto.InjectionItem{
			ID:          chosen.ID,
			Name:        chosen.Name,
			PreDuration: chosen.PreDuration,
			StartTime:   startTime,
			EndTime:     endTime,
		}
	}

	mergedEnvVars := mergeBenchmarkEnvVars(r.containerRepo, benchmark.ID, namespace, logEntry)
	benchmark.EnvVars = mergedEnvVars

	// We deliberately do NOT carry over the original CRD-annotation trace
	// carrier: it was scoped to the chaos-mesh CRD lifetime, the CRD has
	// long since been GC'd by the time we run, and the downstream
	// BuildDatapack executor will mint a fresh trace span anyway. Leaving
	// the carriers nil is the same shape SubmitTaskWithDB sees for any
	// fresh task.
	taskID := fiTask.ID
	buildTask := &dto.UnifiedTask{
		Type:      consts.TaskTypeBuildDatapack,
		Immediate: true,
		Payload: map[string]any{
			consts.BuildBenchmark:        benchmark,
			consts.BuildDatapack:         *updatedItem,
			consts.BuildDatasetVersionID: consts.DefaultInvalidID,
		},
		ParentTaskID:   utils.StringPtr(taskID),
		TraceID:        trace.ID,
		GroupID:        trace.GroupID,
		ProjectID:      trace.ProjectID,
		State:          consts.TaskPending,
		StuckRecovered: true,
	}

	if trace.OTelTraceID != "" && trace.OTelRootSpanID != "" {
		if sc, scErr := tracing.NewRootSpanContext(trace.OTelTraceID, trace.OTelRootSpanID, trace.OTelFlags); scErr == nil {
			rootCtx := oteltrace.ContextWithRemoteSpanContext(ctx, sc)
			buildTask.SetRootTraceCtx(rootCtx)
		} else {
			logEntry.WithError(scErr).Warn("reconciler: failed to reconstruct root SpanContext, skipping carrier injection")
		}
	}

	if r.submitTask == nil {
		return false, fmt.Errorf("submitTask not configured")
	}

	// For traces stuck at fault.injection.started, the FaultInjection
	// parent task is still TaskRunning in the DB. selectBestLastEvent
	// ranks EventFaultInjectionCompleted highest among leaf events, but
	// only TaskCompleted leaves are candidates — leaving the parent at
	// TaskRunning means trace.last_event stays pinned at
	// fault.injection.started even after BuildDatapack/RunAlgorithm/
	// CollectResult complete downstream. Mark the parent TaskCompleted so
	// the next downstream task transition re-derives last_event
	// correctly via updateTraceState.
	//
	// We deliberately update only the task row here, not the trace's
	// last_event field directly. The very next event in this trace is
	// BuildDatapack's state change (which we're about to submit), and
	// that path's updateTraceState will reread all task rows and pick
	// the highest-priority completed event — at which point our just-
	// completed FaultInjection becomes the source for last_event. This
	// avoids tying the reconciler to redisGateway availability and
	// avoids racing with the goroutine-launched updateTraceState used
	// elsewhere.
	if trace.LastEvent == consts.EventFaultInjectionStarted {
		if err := r.db.WithContext(ctx).Model(&model.Task{}).
			Where("id = ? AND state = ?", fiTask.ID, consts.TaskRunning).
			Update("state", consts.TaskCompleted).Error; err != nil {
			return false, fmt.Errorf("advance fault-injection parent task: %w", err)
		}
		fiTask.State = consts.TaskCompleted
		logEntry.WithField("fault_injection_task_id", fiTask.ID).
			Info("advanced FaultInjection parent task to TaskCompleted before BuildDatapack submit")
	}

	// Replica-safe submit. Two reconciler ticks (across replicas or even
	// the same replica racing on a slow submit) can both observe zero
	// BuildDatapack children at the unsynchronized count. Serialize via
	// a row-level write lock on the parent FaultInjection task and
	// re-check inside the transaction; whichever transaction commits
	// first wins and the other one's recheck-zero collapses to "child
	// already exists, bail out". On dialects without FOR UPDATE (sqlite
	// in our tests) the per-DB serial-write lock gives the same effect.
	submitted, err := r.submitIfNoChild(ctx, &fiTask, buildTask, logEntry)
	if err != nil {
		return false, err
	}
	if !submitted {
		return false, nil
	}

	logEntry.WithField("fault_injection_task_id", fiTask.ID).
		Info("stuck trace reconciled: BuildDatapack task submitted")
	return true, nil
}

// recoverStuckRestartPedestal cancels a trace pinned at
// restart.pedestal.started — the shape we see when a worker dies between
// helm-apply and waitForPedestalWorkloadReady. The trace cannot be resumed
// (the dead worker held both rate-limit tokens and the namespace lock in
// memory, and we have no checkpoint of how far helm-apply got), so we
// finalize it as cancelled and free the held resources for the next run.
func (r *StuckTraceReconciler) recoverStuckRestartPedestal(ctx context.Context, trace *model.Trace, logEntry *logrus.Entry) (bool, error) {
	var rpTask model.Task
	err := r.db.WithContext(ctx).
		Where("trace_id = ? AND type = ? AND status != ?",
			trace.ID,
			consts.TaskTypeRestartPedestal,
			consts.CommonDeleted,
		).
		Order("created_at DESC").
		First(&rpTask).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			logEntry.Debug("no RestartPedestal task for stuck trace, skipping")
			return false, nil
		}
		return false, fmt.Errorf("lookup restart-pedestal task: %w", err)
	}

	namespace := namespaceFromRestartPayload(rpTask.Payload)

	now := r.now()
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Task{}).
			Where("id = ? AND state IN ?", rpTask.ID, []consts.TaskState{consts.TaskPending, consts.TaskRescheduled, consts.TaskRunning}).
			Update("state", consts.TaskCancelled).Error; err != nil {
			return fmt.Errorf("cancel restart-pedestal task: %w", err)
		}
		if err := tx.Model(&model.Trace{}).
			Where("id = ? AND state = ?", trace.ID, consts.TraceRunning).
			Updates(map[string]any{
				"state":      consts.TraceFailed,
				"last_event": consts.EventTraceCancelled,
				"end_time":   now,
			}).Error; err != nil {
			return fmt.Errorf("cancel trace: %w", err)
		}
		return nil
	}); err != nil {
		return false, err
	}

	// Release the namespace lock + rate-limit tokens the dead worker was
	// holding. All three releases are best-effort: ReleaseLock fails
	// cleanly on a lock not owned by this trace, ReleaseToken is a no-op
	// on a missing token. Any failure is logged but doesn't fail the
	// recovery — the trace is already finalized, the next run can proceed
	// even if a token leak lingers until its TTL expires.
	if namespace != "" && r.namespaceLockReleaser != nil {
		if err := r.namespaceLockReleaser.ReleaseLock(ctx, namespace, trace.ID); err != nil {
			logEntry.WithError(err).WithField("namespace", namespace).
				Warn("release namespace lock for stuck restart-pedestal trace failed (continuing)")
		}
	}
	if r.restartTokenReleaser != nil {
		if err := r.restartTokenReleaser.ReleaseToken(ctx, rpTask.ID, trace.ID); err != nil {
			logEntry.WithError(err).Warn("release restart-pedestal token failed (continuing)")
		}
	}
	if r.warmingTokenReleaser != nil {
		if err := r.warmingTokenReleaser.ReleaseToken(ctx, rpTask.ID, trace.ID); err != nil {
			logEntry.WithError(err).Warn("release namespace-warming token failed (continuing)")
		}
	}

	logEntry.WithFields(logrus.Fields{
		"restart_pedestal_task_id": rpTask.ID,
		"namespace":                namespace,
	}).Info("stuck restart-pedestal trace cancelled, lock + tokens released")
	return true, nil
}

// namespaceFromRestartPayload extracts the namespace the RestartPedestal
// task was operating against. The guided path sets RestartRequiredNamespace
// (#156); the legacy pool-selection path sets InjectNamespace on the inner
// inject payload after monitor.GetNamespaceToRestart picked one. Returns ""
// if neither is populated — the recovery still proceeds, just without a
// namespace-lock release (the lock will eventually fall out via TTL).
func namespaceFromRestartPayload(raw string) string {
	if raw == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	if v, ok := payload[consts.RestartRequiredNamespace].(string); ok {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	if inner, ok := payload[consts.RestartInjectPayload].(map[string]any); ok {
		if v, ok := inner[consts.InjectNamespace].(string); ok {
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		}
	}
	return ""
}

// submitIfNoChild atomically rechecks "does this FaultInjection task already
// have a BuildDatapack child?" under a row-level write lock on the parent
// task, and only submits the recovery task if not. Returns (true, nil) iff
// it actually submitted; (false, nil) means the CRD-success path or another
// reconciler replica beat us to it.
func (r *StuckTraceReconciler) submitIfNoChild(
	ctx context.Context,
	parent *model.Task,
	buildTask *dto.UnifiedTask,
	logEntry *logrus.Entry,
) (bool, error) {
	submitted := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lockedQuery := tx.Model(&model.Task{}).
			Where("id = ?", parent.ID)
		if r.supportsRowLock() {
			lockedQuery = lockedQuery.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var locked model.Task
		if err := lockedQuery.First(&locked).Error; err != nil {
			return fmt.Errorf("lock parent task: %w", err)
		}

		var existingDatapackCount int64
		if err := tx.Model(&model.Task{}).
			Where("parent_task_id = ? AND type = ? AND status != ?",
				parent.ID,
				consts.TaskTypeBuildDatapack,
				consts.CommonDeleted,
			).
			Count(&existingDatapackCount).Error; err != nil {
			return fmt.Errorf("idempotency check: %w", err)
		}
		if existingDatapackCount > 0 {
			logEntry.Debug("BuildDatapack task already exists for fault-injection task, skipping")
			return nil
		}

		if err := r.submitTask(ctx, tx, r.redisGateway, buildTask); err != nil {
			return fmt.Errorf("submit recovered BuildDatapack task: %w", err)
		}
		submitted = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return submitted, nil
}

// supportsRowLock returns true on dialects that honor SELECT ... FOR UPDATE
// (MySQL, Postgres). SQLite — the in-memory test driver — has implicit
// per-connection serialization for writes and rejects FOR UPDATE syntax
// outright, so we drop the clause there. The transaction itself still
// gives us the recheck-and-submit atomicity we need.
func (r *StuckTraceReconciler) supportsRowLock() bool {
	if r.db == nil || r.db.Dialector == nil {
		return false
	}
	switch r.db.Dialector.Name() {
	case "mysql", "postgres":
		return true
	default:
		return false
	}
}

// stuckGraceWindow is added on top of the inject duration to absorb the
// usual k8s-controller queue + chaos-mesh recovery-check delay. With
// CheckRecovery's 1-minute retry budget on top, two minutes is a safe lower
// bound; we keep it conservative since the reconciler is the slow path.
const stuckGraceWindow = 2 * time.Minute

func intervalSecondsFromConfig() int {
	v := config.GetInt(consts.StuckTraceReconcileIntervalKey)
	if v <= 0 {
		return consts.DefaultStuckTraceReconcileIntervalSecs
	}
	return v
}

func stuckThresholdSecondsFromConfig() int {
	v := config.GetInt(consts.StuckTraceReconcileStuckThresholdKey)
	if v <= 0 {
		return consts.DefaultStuckTraceReconcileStuckSecs
	}
	return v
}

// restartPedestalStuckThresholdSeconds is the (longer) stuck threshold for
// traces parked at restart.pedestal.started. Read on every tick so an etcd
// push applies without a rebuild.
func restartPedestalStuckThresholdSeconds() int {
	v := config.GetInt(consts.StuckTraceReconcileRestartPedestalThresholdKey)
	if v <= 0 {
		return consts.DefaultStuckTraceReconcileRestartPedestalSecs
	}
	return v
}

// decodeTaskPayload deserializes the FaultInjection task's payload (stored
// as a JSON string in the tasks.payload column) back into the map shape the
// rest of the consumer code consumes.
func decodeTaskPayload(t *model.Task) (map[string]any, error) {
	if t.Payload == "" {
		return nil, fmt.Errorf("empty task payload")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(t.Payload), &out); err != nil {
		return nil, fmt.Errorf("unmarshal task payload: %w", err)
	}
	return out, nil
}

// maxGuidedDurationMinutes returns the largest guided_configs[i].Duration in
// the FaultInjection task's payload, in minutes. Defaults to 5 (the guided
// default) if no duration is present.
func maxGuidedDurationMinutes(payload map[string]any) int {
	configs, err := utils.ConvertToType[[]guidedcli.GuidedConfig](payload[consts.InjectGuidedConfigs])
	if err != nil {
		return 5
	}
	longest := 0
	for _, c := range configs {
		if c.Duration != nil && *c.Duration > longest {
			longest = *c.Duration
		}
	}
	if longest == 0 {
		return 5
	}
	return longest
}

// pickInjectionForDatapack chooses the FaultInjection row whose Name will be
// passed into updateInjectionTimestamp. For single-leaf this is trivial.
// For hybrid we pick the row with the latest UpdatedAt — that's the one
// most likely to carry the freshest timestamp data; the BuildDatapack
// pipeline only consumes one InjectionItem per task.
func pickInjectionForDatapack(injections []model.FaultInjection) *model.FaultInjection {
	if len(injections) == 1 {
		return &injections[0]
	}
	bestIdx := 0
	bestTime := injections[0].UpdatedAt
	for i := 1; i < len(injections); i++ {
		if injections[i].UpdatedAt.After(bestTime) {
			bestIdx = i
			bestTime = injections[i].UpdatedAt
		}
	}
	return &injections[bestIdx]
}

// mergeBenchmarkEnvVars rebuilds the BuildDatapack env-var slice: NAMESPACE
// override at the top, then de-duplicated benchmark env vars from
// container_version_env_vars. Mirrors the merge the chaos-webhook
// receiver does (crud/hooks/chaos/handler.go::fireOnce); the reconciler
// reuses the same shape for retries against rows the webhook stamped.
// ListEnvVarsByVersionID failures are tolerated — treat as empty list,
// log and continue.
func mergeBenchmarkEnvVars(repo containerEnvVarLister, benchmarkID int, namespace string, logEntry *logrus.Entry) []dto.ParameterItem {
	var benchEnvVars []dto.ParameterItem
	if repo != nil {
		var err error
		benchEnvVars, err = repo.ListEnvVarsByVersionID(benchmarkID)
		if err != nil {
			logEntry.WithError(err).Warn("list benchmark env vars failed (continuing)")
			benchEnvVars = nil
		}
	}
	merged := make([]dto.ParameterItem, 0, len(benchEnvVars)+1)
	seen := map[string]bool{}
	if namespace != "" {
		nsOverride := dto.ParameterItem{Key: "NAMESPACE", Value: namespace}
		merged = append(merged, nsOverride)
		seen[nsOverride.Key] = true
	}
	for _, ev := range benchEnvVars {
		if seen[ev.Key] {
			continue
		}
		seen[ev.Key] = true
		merged = append(merged, ev)
	}
	return merged
}

// StartStuckTraceReconciler is the fx hook entry point. It launches Run on
// a goroutine. The runOnce guard is instance-scoped (not package-scoped) so
// multiple fx app start/stop cycles in the same process — common in tests
// and any future in-process controller restart — can each spawn the loop
// against a fresh ctx without being silently no-op'd.
func StartStuckTraceReconciler(ctx context.Context, r *StuckTraceReconciler) {
	if r == nil {
		return
	}
	r.runOnce.Do(func() {
		go r.Run(ctx)
	})
}
