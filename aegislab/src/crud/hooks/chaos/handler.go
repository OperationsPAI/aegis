package chaoshooks

import (
	"context"
	"errors"
	"net/http"
	"time"

	"aegis/core/orchestrator/common"
	"aegis/platform/consts"
	"aegis/platform/dto"
	redis "aegis/platform/redis"
	"aegis/platform/tracing"
	"aegis/platform/utils"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
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
		_ = h.recordGate(c.Request.Context(), body.InjectionID, kindSingleton, body.Status)
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
		_ = h.recordGate(c.Request.Context(), body.BatchID, kindBatch, body.AggregatedStatus)
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
// was actually submitted (false on idempotent replay).
func (h *Handler) fireOnce(parentCtx context.Context, id, kind, terminal string, meta *CallerMetadata, startedAt, finishedAt time.Time) (bool, error) {
	if meta == nil || meta.TaskID == "" || meta.TraceID == "" {
		return false, errors.New("caller_metadata missing task_id/trace_id")
	}
	if meta.Benchmark == nil || meta.Datapack == nil {
		return false, errors.New("caller_metadata missing benchmark/datapack")
	}

	claimed, err := h.claimGate(parentCtx, id, kind, terminal)
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

	payload := map[string]any{
		consts.BuildBenchmark:        *meta.Benchmark,
		consts.BuildDatapack:         dp,
		consts.BuildDatasetVersionID: consts.DefaultInvalidID,
	}

	task := &dto.UnifiedTask{
		Type:      consts.TaskTypeBuildDatapack,
		Immediate: true,
		Payload:   payload,
		TraceID:   meta.TraceID,
		GroupID:   meta.GroupID,
		ProjectID: meta.ProjectID,
		UserID:    meta.UserID,
		State:     consts.TaskPending,
	}
	if meta.TaskID != "" {
		task.ParentTaskID = utils.StringPtr(meta.TaskID)
	}

	return true, tracing.WithSpanNamed(parentCtx, "hooks/chaos/"+kind, func(ctx context.Context) error {
		task.SetTraceCtx(ctx)
		return h.submit(ctx, task)
	})
}

// claimGate INSERTs the dedup row; returns true on win, false on conflict.
// (injection_or_batch_id, kind, terminal_status) — design §10.2 +
// ADRs 0006/0007.
func (h *Handler) claimGate(ctx context.Context, id, kind, terminal string) (bool, error) {
	row := HookSubmission{ID: id, Kind: kind, TerminalStatus: terminal, SubmittedAt: time.Now().UTC()}
	res := h.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
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
