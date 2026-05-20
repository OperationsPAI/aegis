package chaoshooks

import (
	"context"
	"errors"
	"net/http"
	"time"

	"aegis/core/orchestrator/common"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"
	redis "aegis/platform/redis"
	"aegis/platform/utils"

	"github.com/gin-gonic/gin"
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
	db     *gorm.DB
	submit TaskSubmitter
}

func NewHandler(db *gorm.DB, redisGateway *redis.Gateway) *Handler {
	return &Handler{
		db: db,
		submit: func(ctx context.Context, task *dto.UnifiedTask) error {
			return common.SubmitTaskWithDB(ctx, db, redisGateway, task)
		},
	}
}

// NewHandlerWithSubmitter is the test seam.
func NewHandlerWithSubmitter(db *gorm.DB, submit TaskSubmitter) *Handler {
	return &Handler{db: db, submit: submit}
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
		// duplicate webhooks remain no-ops.
		_ = h.recordGate(c.Request.Context(), body.CallerMetadata.TaskID, kindSingleton, body.Status)
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
	// decides whether to retry. Record the gate so retries are no-ops.
	if body.AggregatedStatus == statusCancelled || body.AggregatedStatus == statusFailed {
		_ = h.recordGate(c.Request.Context(), body.BatchCallerMetadata.TaskID, kindBatch, body.AggregatedStatus)
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

	row := model.FaultInjection{
		Name:              name,
		ChaosInjectionID:  chaosInjectionID,
		Source:            consts.DatapackSourceInjection,
		GroundtruthSource: "auto",
		Groundtruths:      meta.Groundtruths,
		EngineConfig:      "{}",
		PreDuration:       preDuration,
		// TaskID intentionally nil for --via-chaos shadow rows: aegisctl
		// generates a uuid client-side without persisting a backend tasks
		// row, so the FK to tasks(id) would violate. The legacy CRD flow
		// (k8s_handler.go) populates TaskID from K8s labels where a real
		// tasks row exists; that path doesn't reach this shadow upsert.
		State:  consts.DatapackInjectSuccess,
		Status: consts.CommonEnabled,
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
		t := finishedAt
		row.EndTime = &t
	}

	if err := h.db.WithContext(ctx).Create(&row).Error; err != nil {
		// Lost race with a concurrent winner: re-fetch and return.
		var racer model.FaultInjection
		if rerr := h.db.WithContext(ctx).Where("chaos_injection_id = ?", chaosInjectionID).Take(&racer).Error; rerr == nil {
			return &racer, nil
		}
		return nil, err
	}
	return &row, nil
}

// recordGate is the non-submitting variant: tag a terminal as seen so a
// replay of a non-firing terminal (failed/cancelled) is also a no-op.
func (h *Handler) recordGate(ctx context.Context, id, kind, terminal string) error {
	_, err := h.claimGate(ctx, id, kind, terminal)
	return err
}

func isTerminalSingleton(s string) bool {
	return s == statusSucceeded || s == statusFailed || s == statusCancelled
}

func isTerminalBatch(s string) bool {
	return s == statusSucceeded || s == statusFailed || s == statusCancelled || s == statusPartial
}
