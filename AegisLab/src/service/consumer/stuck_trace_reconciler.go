package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"aegis/config"
	"aegis/consts"
	"aegis/dto"
	redis "aegis/infra/redis"
	"aegis/model"
	container "aegis/module/container"
	"aegis/service/common"
	"aegis/utils"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
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

// NewStuckTraceReconciler builds the production reconciler. The constructor
// is also used by the controller module's fx wiring.
func NewStuckTraceReconciler(
	db *gorm.DB,
	redisGateway *redis.Gateway,
	executionOwner ExecutionOwner,
	injectionOwner InjectionOwner,
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
func (r *StuckTraceReconciler) Run(ctx context.Context) {
	if r == nil || r.db == nil {
		logrus.Warn("StuckTraceReconciler.Run skipped: missing db")
		return
	}
	currentInterval := r.resolveInterval()
	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		processed, err := r.tick(ctx)
		if err != nil {
			logrus.WithError(err).Warn("stuck trace reconcile tick failed")
		} else if processed > 0 {
			logrus.Infof("stuck trace reconcile tick recovered %d trace(s)", processed)
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

func (r *StuckTraceReconciler) resolveInterval() time.Duration {
	v := time.Duration(r.intervalSeconds()) * time.Second
	if v <= 0 {
		v = time.Duration(consts.DefaultStuckTraceReconcileIntervalSecs) * time.Second
	}
	return v
}

// tick runs one reconcile sweep and returns the number of traces it
// successfully recovered.
func (r *StuckTraceReconciler) tick(ctx context.Context) (int, error) {
	stuckSecs := r.stuckThresholdSeconds()
	if stuckSecs <= 0 {
		stuckSecs = consts.DefaultStuckTraceReconcileStuckSecs
	}
	cutoff := r.now().Add(-time.Duration(stuckSecs) * time.Second)

	var traces []model.Trace
	err := r.db.WithContext(ctx).
		Model(&model.Trace{}).
		Where("state = ? AND last_event IN ? AND updated_at < ? AND status != ?",
			consts.TraceRunning,
			[]consts.EventType{consts.EventFaultInjectionStarted, consts.EventFaultInjectionCompleted},
			cutoff,
			consts.CommonDeleted,
		).
		Order("updated_at ASC").
		Order("id ASC").
		Limit(r.maxBatchPerTick).
		Find(&traces).Error
	if err != nil {
		return 0, fmt.Errorf("query stuck traces: %w", err)
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
	return processed, nil
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
		// No timestamps: fall back to (UpdatedAt + max guided duration +
		// grace). This covers the round-3 byte-cluster case where the
		// worker died between CRD-add and CRD-success and updateInjectionTimestamp
		// was never called.
		threshold := inj.UpdatedAt.Add(time.Duration(guidedDuration)*time.Minute + stuckGraceWindow)
		if now.Before(threshold) {
			logEntry.WithField("inj_name", inj.Name).
				Debug("FaultInjection.UpdatedAt + duration still in future, skipping")
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

	// On a fresh FaultInjection row UpdatedAt is the *start* of the
	// injection (the row is written when the chaos CRD is created and
	// updateInjectionTimestamp hasn't yet run). The fault then runs
	// forward from that point for `guidedDuration` minutes, so the
	// abnormal window is [UpdatedAt, UpdatedAt + duration] — emphatically
	// NOT the [UpdatedAt - duration, UpdatedAt] form an earlier draft
	// used. Stored StartTime/EndTime override both.
	startTime := chosen.UpdatedAt
	endTime := chosen.UpdatedAt.Add(time.Duration(guidedDuration) * time.Minute)
	if chosen.StartTime != nil {
		startTime = *chosen.StartTime
	}
	if chosen.EndTime != nil {
		endTime = *chosen.EndTime
	}
	updatedItem, err := r.store.updateInjectionTimestamp(ctx, chosen.Name, startTime, endTime)
	if err != nil {
		// Non-fatal — the FaultInjection row may already have timestamps
		// (the post-CRD-success path wrote them). Build a synthetic item
		// from the row we already have.
		logEntry.WithError(err).Warn("update injection timestamps failed, falling back to existing record")
		fallback := dto.NewInjectionItem(chosen)
		updatedItem = &fallback
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
		ParentTaskID: utils.StringPtr(taskID),
		TraceID:      trace.ID,
		GroupID:      trace.GroupID,
		ProjectID:    trace.ProjectID,
		State:        consts.TaskPending,
	}

	if r.submitTask == nil {
		return false, fmt.Errorf("submitTask not configured")
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
// the FaultInjection task's payload, in minutes. Defaults to 5 (the
// chaos-experiment guided default) if no duration is present.
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

// mergeBenchmarkEnvVars rebuilds the env-var slice that postProcess in
// HandleCRDSucceeded would have constructed: NAMESPACE override at the top,
// then de-duplicated benchmark env vars from container_version_env_vars.
// We tolerate ListEnvVarsByVersionID failures the same way postProcess does
// — treat as empty list, log and continue.
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

// runSingleton guards against double-Run from a misconfigured fx wiring.
var runSingleton sync.Once

// StartStuckTraceReconciler is the fx hook entry point. It launches Run on
// a goroutine and is safe against double-invocation.
func StartStuckTraceReconciler(ctx context.Context, r *StuckTraceReconciler) {
	if r == nil {
		return
	}
	runSingleton.Do(func() {
		go r.Run(ctx)
	})
}
