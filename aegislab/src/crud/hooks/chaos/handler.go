package chaoshooks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"aegis/core/orchestrator/common"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"
	redis "aegis/platform/redis"
	"aegis/platform/utils"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TaskSubmitter is the seam tests substitute for common.SubmitTaskWithDB.
// The production implementation lives in NewHandler.
type TaskSubmitter func(ctx context.Context, task *dto.UnifiedTask) error

// LockReleaser releases the monitor:ns:<namespace> lock a fault-injection
// trace holds. The seam keeps the redis dependency out of the test rig. nil
// is a no-op (tests, or the via-aegisctl path that never acquired the lock).
type LockReleaser func(ctx context.Context, namespace, traceID string) error

// terminal status values from §10.2.
const (
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
	statusCancelled = "cancelled"
	statusPartial   = "partial"

	kindSingleton = "singleton"
	kindBatch     = "batch"
)

type Handler struct {
	db          *gorm.DB
	submit      TaskSubmitter
	releaseLock LockReleaser
}

func NewHandler(db *gorm.DB, redisGateway *redis.Gateway) *Handler {
	return &Handler{
		db: db,
		submit: func(ctx context.Context, task *dto.UnifiedTask) error {
			return common.SubmitTaskWithDB(ctx, db, redisGateway, task)
		},
		releaseLock: redisLockReleaser(redisGateway),
	}
}

// NewHandlerWithSubmitter is the test seam.
func NewHandlerWithSubmitter(db *gorm.DB, submit TaskSubmitter) *Handler {
	return &Handler{db: db, submit: submit}
}

// redisLockReleaser clears the trace_id on monitor:ns:<namespace> when the
// trace still owns it, matching state.LockStore.Release. The orchestrator
// hands lock ownership to this webhook receiver after dispatch (see
// fault_injection.go); on a non-firing terminal (failed/cancelled) nothing
// else releases it, so without this the key leaks until its TTL.
func redisLockReleaser(g *redis.Gateway) LockReleaser {
	if g == nil {
		return nil
	}
	return func(ctx context.Context, namespace, traceID string) error {
		if namespace == "" || traceID == "" {
			return nil
		}
		key := fmt.Sprintf(consts.NamespaceKeyPattern, namespace)
		owner, err := g.HashGet(ctx, key, "trace_id")
		if err != nil && !errors.Is(err, goredis.Nil) {
			return err
		}
		if owner != "" && owner != traceID {
			return nil
		}
		return g.HashSet(ctx, key, map[string]any{
			"end_time": time.Now().UTC().Unix(),
			"trace_id": "",
		})
	}
}

// releaseNamespaceLock is the non-firing-terminal cleanup: free the ns lock
// the inject trace held. Best-effort — a release failure must not fail the
// webhook (the lock TTL is the backstop), so errors are logged not returned.
func (h *Handler) releaseNamespaceLock(ctx context.Context, meta *CallerMetadata) {
	if h.releaseLock == nil || meta == nil || meta.Namespace == "" || meta.TraceID == "" {
		return
	}
	if err := h.releaseLock(ctx, meta.Namespace, meta.TraceID); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"namespace": meta.Namespace,
			"trace_id":  meta.TraceID,
		}).Warn("chaos hook: release namespace lock on terminal-failed inject")
	}
}

// completeFaultInjectionTask advances the dispatcher-owned FaultInjection
// task from TaskRunning to TaskCompleted so a TraceTypeFaultInjection trace
// can finalize. Guarded on state=TaskRunning, so it is idempotent (a replay
// or a backend path that already terminalised the task is a no-op) and a
// no-op on the aegisctl --via-chaos path that never persisted a task row.
// Best-effort: a failure leaves the task Running for the stuck reconciler to
// recover, so it must not abort the BuildDatapack submission.
func (h *Handler) completeFaultInjectionTask(ctx context.Context, taskID string) {
	if taskID == "" {
		return
	}
	res := h.db.WithContext(ctx).Model(&model.Task{}).
		Where("id = ? AND state = ?", taskID, consts.TaskRunning).
		Update("state", consts.TaskCompleted)
	if res.Error != nil {
		logrus.WithError(res.Error).WithField("task_id", taskID).
			Warn("chaos hook: failed to complete fault-injection task (stuck reconciler is the backstop)")
	}
}

// Singleton handles `POST /api/v1/hooks/chaos`. Mirrors today's
// HandleCRDSucceeded post-processing (k8s_handler.go) for the singleton path.
func (h *Handler) Singleton(c *gin.Context) {
	var body SingletonWebhook
	if err := c.ShouldBindJSON(&body); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "invalid webhook body: "+err.Error())
		return
	}

	// W3C trace context (ADR-0005): rejoin the campaign-side trace before
	// any downstream submission so BuildDatapack hangs off the same trace
	// as the original Injection request.
	parentCtx := otel.GetTextMapPropagator().Extract(
		c.Request.Context(),
		propagation.HeaderCarrier(c.Request.Header),
	)

	if !isTerminalSingleton(body.Status) {
		dto.ErrorResponse(c, http.StatusBadRequest, "non-terminal status: "+body.Status)
		return
	}

	if body.Status != statusSucceeded {
		// failed / cancelled: no BuildDatapack downstream, matching today's
		// HandleCRDFailed in k8s_handler.go. We still record the gate so
		// duplicate webhooks remain no-ops, and release the namespace lock
		// ownership the dispatcher handed us so the ns isn't leaked.
		_ = h.recordGate(c.Request.Context(), body.CallerMetadata.TaskID, kindSingleton, body.Status)
		h.releaseNamespaceLock(c.Request.Context(), &body.CallerMetadata)
		dto.SuccessResponse(c, gin.H{"accepted": true, "submitted": false})
		return
	}

	fired, err := h.fireOnce(parentCtx, body.InjectionID, kindSingleton, body.Status, &body.CallerMetadata, body.StartedAt, body.FinishedAt)
	if err != nil {
		logrus.WithError(err).WithField("injection_id", body.InjectionID).
			Error("chaos hook singleton: downstream submit failed")
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	dto.SuccessResponse(c, gin.H{"accepted": true, "submitted": fired})
}

// Batch handles `POST /api/v1/hooks/chaos-batch`. Mirrors today's
// FaultBatchManager N-of-N completion (fault_injection.go + k8s_handler.go
// hybrid branch), but driven by the aggregated_status the webhook already
// resolved on the aegis-chaos side (ADR-0002, ADR-0006).
func (h *Handler) Batch(c *gin.Context) {
	var body BatchWebhook
	if err := c.ShouldBindJSON(&body); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "invalid webhook body: "+err.Error())
		return
	}

	parentCtx := otel.GetTextMapPropagator().Extract(
		c.Request.Context(),
		propagation.HeaderCarrier(c.Request.Header),
	)

	if !isTerminalBatch(body.AggregatedStatus) {
		dto.ErrorResponse(c, http.StatusBadRequest, "non-terminal aggregated_status: "+body.AggregatedStatus)
		return
	}

	// cancelled / failed: nothing fires downstream — the campaign step
	// decides whether to retry. Record the gate so retries are no-ops and
	// release the namespace lock so a terminal-failed batch doesn't leak it.
	if body.AggregatedStatus == statusCancelled || body.AggregatedStatus == statusFailed {
		_ = h.recordGate(c.Request.Context(), body.BatchCallerMetadata.TaskID, kindBatch, body.AggregatedStatus)
		h.releaseNamespaceLock(c.Request.Context(), &body.BatchCallerMetadata)
		dto.SuccessResponse(c, gin.H{"accepted": true, "submitted": false})
		return
	}

	// succeeded / partial both fire one BuildDatapack for the batch — the
	// legacy hybrid path uses batchID as the injection name (k8s_handler.go
	// postProcess(parsedLabels.batchID)). Partial fires too because the
	// campaign owns the policy for what partial means.
	fired, err := h.fireOnce(parentCtx, body.BatchID, kindBatch, body.AggregatedStatus, &body.BatchCallerMetadata, body.StartedAt, body.FinishedAt)
	if err != nil {
		logrus.WithError(err).WithField("batch_id", body.BatchID).
			Error("chaos hook batch: downstream submit failed")
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	dto.SuccessResponse(c, gin.H{"accepted": true, "submitted": fired})
}

// fireOnce is the shared submit path. Returns whether a downstream task
// was actually submitted (false on idempotent replay). The gate is keyed on
// meta.TaskID — the campaign-side UnifiedTask ID — because the legacy CRD
// watcher path knows that same value via K8s labels but does not know the
// chaos-service ULID. Keying on TaskID lets both paths collide cleanly when
// the dispatcher and CRD informer race for the same conceptual injection.
func (h *Handler) fireOnce(parentCtx context.Context, id, kind, terminal string, meta *CallerMetadata, startedAt, finishedAt time.Time) (bool, error) {
	if meta == nil || meta.TaskID == "" || meta.TraceID == "" {
		return false, errors.New("caller_metadata missing task_id/trace_id")
	}
	if meta.Benchmark == nil || meta.Datapack == nil {
		return false, errors.New("caller_metadata missing benchmark/datapack")
	}

	claimed, err := h.claimGate(parentCtx, meta.TaskID, kind, terminal)
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, nil
	}

	// The dispatcher leaves the FaultInjection task TaskRunning by design and
	// hands trace bookkeeping to this webhook (fault_injection.go). Nothing
	// downstream advances it, so a TraceTypeFaultInjection trace's
	// inferTraceState never observes hasActiveOrPendingTasks==false and the
	// trace sits Running until the inject loop force-cancels the namespace.
	// Advance it here, mirroring stuck_trace_reconciler.go's pre-BuildDatapack
	// transition. Independent of the submit below: a completion failure must
	// not abort BuildDatapack (the stuck reconciler is the backstop), and a
	// submit failure must not leave the task Running.
	h.completeFaultInjectionTask(parentCtx, meta.TaskID)

	// Synthesise the InjectionItem timestamps from the webhook so
	// BuildDatapack picks them up the same way HandleCRDSucceeded does
	// after updateInjectionTimestamp.
	dp := *meta.Datapack
	if !startedAt.IsZero() {
		dp.StartTime = startedAt
	}
	if !finishedAt.IsZero() {
		dp.EndTime = finishedAt
	}
	// One-shot chaos (PodKill, PodFailure with grace 0) completes the CR
	// almost instantly, so finishedAt collapses to ~startedAt and the
	// abnormal window never accumulates post-fault traffic — BuildDatapack's
	// CH freshness probe (build_datapack.go) is satisfied immediately and
	// prepare_inputs.py reads a near-empty abnormal_traces window. Duration
	// chaos (NetworkDelay etc.) runs the CR for the full FixedAbnormalWindow,
	// so finishedAt already sits at the planned end. Clamp EndTime up to the
	// planned abnormal-window end so both paths defer the build until the
	// post-fault observation window has elapsed.
	dp.EndTime = clampAbnormalWindowEnd(dp.StartTime, dp.EndTime)

	// chaos webhook path (id is the chaos-service ULID) needs a shadow
	// fault_injections row so audit + BuildDatapack's FI-by-ID lookup work.
	// Legacy CRD path leaves id empty and keeps its own owning row.
	if id != "" {
		shadow, err := h.getOrCreateShadowFaultInjection(parentCtx, id, meta, startedAt, finishedAt)
		if err != nil {
			return false, err
		}
		if shadow != nil {
			dp.ID = shadow.ID
			dp.Name = shadow.Name
			if dp.PreDuration == 0 {
				dp.PreDuration = shadow.PreDuration
			}
		}
	}

	// Mirror k8s_handler.go HandleCRDSucceeded: prepend NAMESPACE env var
	// override onto benchmark.EnvVars so the BuildDatapack container resolves
	// os.environ["NAMESPACE"] to the actual injected namespace. Legacy CRD
	// path does this via parsedAnnotations.benchmark.EnvVars; chaos webhook
	// uses the concrete namespace stamped into caller_metadata by the
	// dispatcher.
	benchCopy := *meta.Benchmark
	if meta.Namespace != "" {
		nsOverride := dto.ParameterItem{Key: "NAMESPACE", Value: meta.Namespace}
		merged := make([]dto.ParameterItem, 0, len(benchCopy.EnvVars)+1)
		merged = append(merged, nsOverride)
		for _, ev := range benchCopy.EnvVars {
			if ev.Key != "NAMESPACE" {
				merged = append(merged, ev)
			}
		}
		benchCopy.EnvVars = merged
	}

	payload := map[string]any{
		consts.BuildBenchmark:        benchCopy,
		consts.BuildDatapack:         dp,
		consts.BuildDatasetVersionID: consts.DefaultInvalidID,
	}

	task := &dto.UnifiedTask{
		Type:             consts.TaskTypeBuildDatapack,
		Immediate:        true,
		Payload:          payload,
		ParentTaskID:     utils.StringPtr(meta.TaskID),
		TraceID:          meta.TraceID,
		GroupID:          meta.GroupID,
		ProjectID:        meta.ProjectID,
		UserID:           meta.UserID,
		State:            consts.TaskPending,
		RootTraceCarrier: meta.RootTraceCarrier,
	}
	if !meta.HasBackendTask {
		task.Extra = map[consts.TaskExtra]any{
			consts.TaskExtraParentSubmittedByAegisctlViaChaos: true,
		}
	}

	tracer := otel.Tracer("rcabench/task")
	return true, func() error {
		ctx, span := tracer.Start(parentCtx, "hooks/chaos",
			oteltrace.WithAttributes(attribute.String("hook.kind", kind)),
		)
		defer span.End()
		task.SetTraceCtx(ctx)
		return h.submit(ctx, task)
	}()
}

// ClaimBuildDatapackGate is the legacy-CRD entry point into the same gate
// the chaos-webhook handler claims via fireOnce. §11 step 5b coexistence
// requires both paths claim the same (task_id, kind) row so a race between
// the in-process CRD informer and the chaos-service webhook cannot fire two
// BuildDatapack tasks for one conceptual injection. Returns true when the
// caller won the race and should proceed with BuildDatapack submission.
func ClaimBuildDatapackGate(ctx context.Context, db *gorm.DB, taskID string, isHybrid bool, terminal string) (bool, error) {
	kind := kindSingleton
	if isHybrid {
		kind = kindBatch
	}
	row := HookSubmission{ID: taskID, Kind: kind, TerminalStatus: terminal, SubmittedAt: time.Now().UTC()}
	res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// claimGate INSERTs the dedup row; returns true on win, false on conflict.
// PK is (task_id, kind) — design §11 step 4 prereq: only one downstream
// BuildDatapack per fault, regardless of which terminal status
// (succeeded/partial/failed/cancelled) arrives first.
func (h *Handler) claimGate(ctx context.Context, id, kind, terminal string) (bool, error) {
	row := HookSubmission{ID: id, Kind: kind, TerminalStatus: terminal, SubmittedAt: time.Now().UTC()}
	res := h.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// getOrCreateShadowFaultInjection materialises the downstream-shadow
// fault_injections row keyed by chaos_injection_id. Returns nil on empty
// chaosInjectionID so the legacy CRD path is unaffected if this is ever
// reached with an unkeyed payload.
func (h *Handler) getOrCreateShadowFaultInjection(ctx context.Context, chaosInjectionID string, meta *CallerMetadata, startedAt, finishedAt time.Time) (*model.FaultInjection, error) {
	if chaosInjectionID == "" {
		return nil, nil
	}

	var existing model.FaultInjection
	res := h.db.WithContext(ctx).Where("chaos_injection_id = ?", chaosInjectionID).Take(&existing)
	if res.Error == nil {
		return &existing, nil
	}
	if !errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return nil, res.Error
	}

	name := ""
	preDuration := 0
	if meta.Datapack != nil {
		name = meta.Datapack.Name
		preDuration = meta.Datapack.PreDuration
	}
	if name == "" {
		name = meta.TaskID
	}
	if preDuration == 0 {
		preDuration = meta.PreDuration
	}

	engineConfig := meta.EngineConfig
	if engineConfig == "" {
		engineConfig = "{}"
	}
	row := model.FaultInjection{
		Name:              name,
		ChaosInjectionID:  chaosInjectionID,
		Source:            consts.DatapackSourceInjection,
		GroundtruthSource: "auto",
		Groundtruths:      meta.Groundtruths,
		EngineConfig:      engineConfig,
		PreDuration:       preDuration,
		State:             consts.DatapackInjectSuccess,
		Status:            consts.CommonEnabled,
	}
	// Backend dispatcher persists the task row before POSTing; the chaos
	// webhook's shadow FI should FK back to it so audit + lineage queries
	// work. aegisctl --via-chaos generates a client-side UUID without
	// persisting and leaves HasBackendTask=false.
	if meta.HasBackendTask && meta.TaskID != "" {
		tid := meta.TaskID
		row.TaskID = &tid
	}
	if meta.Benchmark != nil && meta.Benchmark.ID != 0 {
		bid := meta.Benchmark.ID
		row.BenchmarkID = &bid
	}
	if !startedAt.IsZero() {
		t := startedAt
		row.StartTime = &t
	}
	if !finishedAt.IsZero() {
		t := clampAbnormalWindowEnd(startedAt, finishedAt)
		row.EndTime = &t
	}

	createErr := h.db.WithContext(ctx).Create(&row).Error
	if createErr != nil && row.TaskID != nil {
		// Defensive: HasBackendTask was set but the parent row vanished
		// (race with a DELETE / GC), or the task_id was bogus. Retry once
		// with TaskID=nil so the shadow row still lands; lose the lineage
		// link rather than the whole hook delivery.
		logrus.WithError(createErr).WithFields(logrus.Fields{
			"chaos_injection_id": chaosInjectionID,
			"task_id":            *row.TaskID,
		}).Warn("shadow FI retry without TaskID due to FK violation on backend dispatcher path")
		row.ID = 0
		row.TaskID = nil
		createErr = h.db.WithContext(ctx).Create(&row).Error
	}
	if createErr != nil {
		// Lost race with a concurrent winner: re-fetch and return.
		var racer model.FaultInjection
		if rerr := h.db.WithContext(ctx).Where("chaos_injection_id = ?", chaosInjectionID).Take(&racer).Error; rerr == nil {
			return &racer, nil
		}
		return nil, createErr
	}
	return &row, nil
}

// recordGate is the non-submitting variant: tag a terminal as seen so a
// replay of a non-firing terminal (failed/cancelled) is also a no-op.
func (h *Handler) recordGate(ctx context.Context, id, kind, terminal string) error {
	_, err := h.claimGate(ctx, id, kind, terminal)
	return err
}

// clampAbnormalWindowEnd returns the later of the observed end and the
// planned abnormal-window end (start + FixedAbnormalWindowSeconds). The
// planned window is the protocol invariant every chaos spec is pinned to
// (GuidedToChaosParams sets duration_s = FixedAbnormalWindowSeconds);
// duration-based chaos already finishes there, so its end is unchanged.
// A zero start (legacy CRD path that omits timestamps) is left untouched.
func clampAbnormalWindowEnd(start, end time.Time) time.Time {
	if start.IsZero() {
		return end
	}
	planned := start.Add(consts.FixedAbnormalWindowSeconds * time.Second)
	if planned.After(end) {
		return planned
	}
	return end
}

func isTerminalSingleton(s string) bool {
	return s == statusSucceeded || s == statusFailed || s == statusCancelled
}

func isTerminalBatch(s string) bool {
	return s == statusSucceeded || s == statusFailed || s == statusCancelled || s == statusPartial
}
