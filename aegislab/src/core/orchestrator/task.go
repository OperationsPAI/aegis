package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"
	redisinfra "aegis/platform/redis"
	"aegis/platform/tracing"
	"aegis/platform/model"
	"aegis/core/orchestrator/common"
	"aegis/platform/utils"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	goredis "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

// -----------------------------------------------------------------------------
// Constants and Global Variables
// -----------------------------------------------------------------------------

// Redis key constants for task queues and indexes
const (
	DelayedQueueKey    = "task:delayed"          // Sorted set for delayed tasks
	ReadyQueueKey      = "task:ready"            // List for ready-to-execute tasks
	DeadLetterKey      = "task:dead"             // Sorted set for failed tasks
	TaskIndexKey       = "task:index"            // Hash mapping task IDs to their queue
	ConcurrencyLockKey = "task:concurrency_lock" // Counter for concurrency control
	MaxConcurrency     = 20                      // Maximum concurrent tasks
)

// Prometheus metrics for task monitoring
var (
	// Counter for tracking processed tasks by type and status
	tasksProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "task_processed_total",
		Help: "Total number of processed tasks",
	}, []string{"type", "status"})

	// Histogram for measuring task duration by type
	taskDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "task_duration_seconds",
		Help:    "Task processing duration distribution",
		Buckets: []float64{0.1, 0.5, 1, 5, 10, 30},
	}, []string{"type"})
)

// Task cancellation registry
var (
	taskCancelFuncs      = make(map[string]context.CancelFunc) // Maps task IDs to their cancel functions
	taskCancelFuncsMutex sync.RWMutex                          // Mutex to protect the map
)

// -----------------------------------------------------------------------------
// Event Publishing Options
// -----------------------------------------------------------------------------

type eventPublishOptions struct {
	callerLevel int
}

type eventPublishOption func(*eventPublishOptions)

func withCallerLevel(level int) eventPublishOption {
	return func(opts *eventPublishOptions) {
		opts.callerLevel = level
	}
}

// -----------------------------------------------------------------------------
// Task State Update Notification
// -----------------------------------------------------------------------------

// taskStateUpdate encapsulates all information needed to update and notify task state changes
type taskStateUpdate struct {
	traceID      string
	taskID       string
	taskType     consts.TaskType
	taskState    consts.TaskState
	message      string
	event        *dto.TraceStreamEvent // Optional: custom event to publish
	db           *gorm.DB
	redisGateway *redisinfra.Gateway
}

// newTaskStateUpdate creates a basic TaskStateUpdate with required fields
func newTaskStateUpdate(traceID, taskID string, taskType consts.TaskType, taskState consts.TaskState, message string) *taskStateUpdate {
	return &taskStateUpdate{
		traceID:   traceID,
		taskID:    taskID,
		taskType:  taskType,
		taskState: taskState,
		message:   message,
	}
}

// taskCompleted creates a TaskStateUpdate for completed tasks with standard message
func taskCompleted(task *dto.UnifiedTask) *taskStateUpdate {
	return newTaskStateUpdate(
		task.TraceID,
		task.TaskID,
		task.Type,
		consts.TaskCompleted,
		fmt.Sprintf(consts.TaskMsgCompleted, task.TaskID),
	)
}

// taskCompletedWithEvent creates a TaskStateUpdate for completed tasks with custom event
func taskCompletedWithEvent(task *dto.UnifiedTask, eventType consts.EventType, payload any) *taskStateUpdate {
	return taskCompleted(task).withEvent(eventType, payload)
}

// withEvent adds a custom event to the TaskStateUpdate
func (u *taskStateUpdate) withEvent(eventType consts.EventType, payload any) *taskStateUpdate {
	u.event = &dto.TraceStreamEvent{
		TaskID:    u.taskID,
		TaskType:  u.taskType,
		EventName: eventType,
		Payload:   payload,
	}
	return u
}

// withSimpleEvent adds a simple event with just an event type (no payload)
func (u *taskStateUpdate) withSimpleEvent(eventType consts.EventType) *taskStateUpdate {
	return u.withEvent(eventType, nil)
}

func (u *taskStateUpdate) withDB(db *gorm.DB) *taskStateUpdate {
	u.db = db
	return u
}

func (u *taskStateUpdate) withRedis(gateway *redisinfra.Gateway) *taskStateUpdate {
	u.redisGateway = gateway
	return u
}

// StartScheduler starts the scheduler that moves tasks from delayed to ready queue
func StartScheduler(ctx context.Context, redisGateway *redisinfra.Gateway) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			processDelayedTasks(ctx, redisGateway)
		case <-ctx.Done():
			return
		}
	}
}

// processDelayedTasks moves tasks from delayed queue to ready queue when their time arrives
func processDelayedTasks(ctx context.Context, redisGateway *redisinfra.Gateway) {
	result, err := redisGateway.ProcessDelayedTasks(ctx)

	if err != nil && err != goredis.Nil {
		logrus.Errorf("scheduler error: %v", err)
		return
	}

	for _, taskData := range result {
		var task dto.UnifiedTask
		if err := json.Unmarshal([]byte(taskData), &task); err != nil {
			logrus.Warnf("failed to parse task: %v", err)
			continue
		}

		if task.CronExpr != "" {
			nextTime, err := common.CronNextTime(task.CronExpr)
			if err != nil {
				logrus.Warnf("invalid cron expr: %v", err)
				if err := redisGateway.HandleCronRescheduleFailure(ctx, []byte(taskData)); err != nil {
					logrus.Errorf("failed to handle cron reschedule failure: %v", err)
				}
				continue
			}

			task.ExecuteTime = nextTime.Unix()
			taskData, err := json.Marshal(task)
			if err != nil {
				logrus.Errorf("failed to marshal cron task %s: %v", task.TaskID, err)
				return
			}

			if err := redisGateway.SubmitDelayedTask(ctx, taskData, task.TaskID, task.ExecuteTime); err != nil {
				logrus.Errorf("failed to reschedule cron task %s: %v", task.TaskID, err)
				err := redisGateway.HandleCronRescheduleFailure(ctx, []byte(taskData))
				if err != nil {
					logrus.Errorf("failed to handle cron reschedule failure: %v", err)
				}
			} else {
				common.EmitTaskScheduled(ctx, redisGateway, &task, task.ExecuteTime, dto.TaskScheduledReasonCronNext)
			}
		}
	}
}

// -----------------------------------------------------------------------------
// Task Consumption and Processing
// -----------------------------------------------------------------------------

// ConsumeTasks starts a consumer that processes tasks from the ready queue
func ConsumeTasks(ctx context.Context, deps RuntimeDeps) {
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("consumer panic: %v", r)
		}
	}()
	logrus.Info("Starting consume tasks")

	for {
		if !deps.RedisGateway.AcquireConcurrencyLock(ctx) {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		taskData, err := deps.RedisGateway.GetTask(ctx, 30*time.Second)
		if err != nil {
			deps.RedisGateway.ReleaseConcurrencyLock(ctx)
			if err == goredis.Nil {
				continue
			}
			logrus.Errorf("BRPop error: %v", err)
			time.Sleep(time.Second)
			continue
		}

		go processTask(ctx, taskData, deps)
	}
}

// processTask handles a task from the queue
func processTask(ctx context.Context, taskData string, deps RuntimeDeps) {
	defer deps.RedisGateway.ReleaseConcurrencyLock(ctx)
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("task panic: %v\n%s", r, debug.Stack())
		}
	}()

	var task dto.UnifiedTask
	if err := json.Unmarshal([]byte(taskData), &task); err != nil {
		logrus.Warnf("invalid task data: %v", err)
		return
	}

	// Previously, ctx is an empty context.
	// extractContext rebuilds the trace's root SpanContext (from carrier
	// or DB) and opens a stage + task span underneath it.
	stageCtx, taskCtx := extractContext(&task, deps.DB)
	stageSpan := trace.SpanFromContext(stageCtx)
	defer stageSpan.End()

	taskSpan := trace.SpanFromContext(taskCtx)
	defer taskSpan.End()

	startTime := time.Now()

	tasksProcessed.WithLabelValues(consts.GetTaskTypeName(task.Type), "started").Inc()

	executeTaskWithRetry(taskCtx, &task, deps)

	taskDuration.WithLabelValues(consts.GetTaskTypeName(task.Type)).Observe(time.Since(startTime).Seconds())
}

// preInstrumentationWarnOnce ensures the "pre-instrumentation trace" warning
// fires at most once per worker process for legacy traces missing the
// persisted OTel root SpanContext (rows created before the 2026-05 schema
// change). The warning is informational — the trace still executes, just
// without the unified-TraceID coverage.
var preInstrumentationWarnOnce sync.Once

// extractContext builds the stage and task contexts from a task.
//
// Context hierarchy (new):
//  1. Reconstruct the trace's persisted root SpanContext (from
//     task.RootTraceCarrier if present, else from the Trace row's
//     OTelTraceID/OTelRootSpanID columns).
//  2. Open a stage span as a child of the root SpanContext.
//  3. Open a task ("consume <Type>") span as a child of the stage span.
//
// Graceful degradation: traces created before the OTel columns existed have
// neither carrier nor persisted IDs. Fall back to per-pickup synthesis (one
// TraceId per pickup) and emit a once-per-worker warning so partial
// coverage is visible.
func extractContext(task *dto.UnifiedTask, db *gorm.DB) (context.Context, context.Context) {
	var stageCtx context.Context

	rootCtx, ok := resolveRootContext(task, db)
	if !ok {
		preInstrumentationWarnOnce.Do(func() {
			logrus.WithField("trace_id", task.TraceID).
				Warn("trace pre-instrumentation; OTel coverage incomplete (no RootTraceCarrier and no persisted OTel columns)")
		})
		groupCtx := task.GetGroupCtx()
		var fallbackSpan trace.Span
		stageCtx, fallbackSpan = otel.Tracer("rcabench/trace").Start(groupCtx,
			fmt.Sprintf("stage/%s", consts.GetTaskTypeName(task.Type)),
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(
				attribute.String("aegis.trace_id", task.TraceID),
				attribute.Bool("aegis.pre_instrumentation", true),
			),
		)
		fallbackSpan.SetStatus(codes.Ok, fmt.Sprintf("Started processing task trace %s", task.TraceID))
	} else {
		stageAttrs := []attribute.KeyValue{
			attribute.String("aegis.trace_id", task.TraceID),
			attribute.String("aegis.task_id", task.TaskID),
			attribute.String("aegis.task_type", consts.GetTaskTypeName(task.Type)),
		}
		if task.StuckRecovered {
			stageAttrs = append(stageAttrs, attribute.Bool("aegis.stuck_recovered", true))
		}
		stageCtx, _ = otel.Tracer("rcabench/trace").Start(rootCtx,
			fmt.Sprintf("stage/%s", consts.GetTaskTypeName(task.Type)),
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(stageAttrs...),
		)
	}

	// Keep the per-pickup TraceCarrier populated for K8s annotation paths
	// that still propagate via the legacy carrier slot.
	task.SetTraceCtx(stageCtx)

	taskCtx, _ := otel.Tracer("rcabench/task").Start(stageCtx,
		fmt.Sprintf("consume %s task", consts.GetTaskTypeName(task.Type)),
		trace.WithAttributes(
			attribute.String("task_id", task.TaskID),
			attribute.String("task_type", consts.GetTaskTypeName(task.Type)),
		))

	return stageCtx, taskCtx
}

// resolveRootContext reconstructs the trace's root SpanContext from the
// task's RootTraceCarrier (preferred — no DB hit) or from the persisted
// Trace row's OTel columns. Returns (ctx, true) when the root is available;
// (background, false) for pre-instrumentation rows.
func resolveRootContext(task *dto.UnifiedTask, db *gorm.DB) (context.Context, bool) {
	if len(task.RootTraceCarrier) > 0 {
		ctx := task.GetRootTraceCtx(context.Background())
		sc := trace.SpanContextFromContext(ctx)
		if sc.IsValid() {
			return ctx, true
		}
	}

	if db == nil || task.TraceID == "" {
		return context.Background(), false
	}

	var row model.Trace
	if err := db.Select("o_tel_trace_id", "o_tel_root_span_id", "o_tel_flags").
		Where("id = ?", task.TraceID).
		First(&row).Error; err != nil {
		return context.Background(), false
	}
	if row.OTelTraceID == "" || row.OTelRootSpanID == "" {
		return context.Background(), false
	}

	sc, err := tracing.NewRootSpanContext(row.OTelTraceID, row.OTelRootSpanID, row.OTelFlags)
	if err != nil {
		logrus.WithError(err).WithField("trace_id", task.TraceID).
			Warn("failed to reconstruct OTel root SpanContext from DB row")
		return context.Background(), false
	}
	return trace.ContextWithRemoteSpanContext(context.Background(), sc), true
}

// executeTaskWithRetry attempts to execute a task with retry logic
func executeTaskWithRetry(ctx context.Context, task *dto.UnifiedTask, deps RuntimeDeps) {
	retryCtx, retryCancel := context.WithCancel(ctx)
	registerCancelFunc(task.TaskID, retryCancel)
	defer retryCancel()
	defer unregisterCancelFunc(task.TaskID)

	span := trace.SpanFromContext(ctx)

	errs := make([]error, 0)
	// Fixed-interval backoff using RetryPolicy.BackoffSec between attempts
	for attempt := 0; attempt <= task.RetryPolicy.MaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-retryCtx.Done():
				logrus.Infof("Task %s canceled during retry", task.TaskID)
				return
			case <-time.After(time.Duration(task.RetryPolicy.BackoffSec) * time.Second):
			}
		}

		ctxWithCancel, cancel := context.WithCancel(ctx)
		_ = cancel

		err := dispatchTask(ctxWithCancel, task, deps)
		if err == nil {
			tasksProcessed.WithLabelValues(consts.GetTaskTypeName(task.Type), "success").Inc()
			span.SetStatus(codes.Ok, fmt.Sprintf("Task %s of type %s completed successfully after %d attempts",
				task.TaskID, consts.GetTaskTypeName(task.Type), attempt+1))
			return
		}

		errs = append(errs, err)

		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			logrus.WithField("task_id", task.TaskID).Info("Task canceled")
			span.RecordError(err)
			return
		}

		message := fmt.Sprintf("Attempt %d failed: %v", attempt+1, err)
		span.AddEvent(message)
		logrus.WithField("task_id", task.TaskID).Warn(message)
		publishEvent(deps.RedisGateway, ctx, fmt.Sprintf(consts.StreamTraceLogKey, task.TraceID), dto.TraceStreamEvent{
			TaskID:    task.TaskID,
			TaskType:  task.Type,
			EventName: consts.EventTaskRetryStatus,
			Payload: dto.InfoPayloadTemplate{
				State: consts.GetTaskStateName(consts.TaskError),
				Msg:   err.Error(),
			},
		})
	}

	tasksProcessed.WithLabelValues(consts.GetTaskTypeName(task.Type), "failed").Inc()

	message := fmt.Sprintf("Task failed after %d attempts, errors: [%v]", task.RetryPolicy.MaxAttempts, errs)
	handleFinalFailure(ctx, deps.RedisGateway, task, message)

	// Simple usage: no custom event needed
	updateTaskState(ctx, newTaskStateUpdate(
		task.TraceID,
		task.TaskID,
		task.Type,
		consts.TaskError,
		message,
	).withDB(deps.DB).withRedis(deps.RedisGateway))
}

// -----------------------------------------------------------------------------
// Task Cancellation and Control Functions
// -----------------------------------------------------------------------------

// registerCancelFunc stores a task's cancel function
func registerCancelFunc(taskID string, cancel context.CancelFunc) {
	taskCancelFuncsMutex.Lock()
	defer taskCancelFuncsMutex.Unlock()
	taskCancelFuncs[taskID] = cancel
}

// unregisterCancelFunc removes a task's cancel function
func unregisterCancelFunc(taskID string) {
	taskCancelFuncsMutex.Lock()
	defer taskCancelFuncsMutex.Unlock()
	delete(taskCancelFuncs, taskID)
}

// handleFinalFailure moves a failed task to the dead letter queue
func handleFinalFailure(ctx context.Context, redisGateway *redisinfra.Gateway, task *dto.UnifiedTask, errMsg string) {
	taskData, err := json.Marshal(task)
	if err != nil {
		logrus.Errorf("failed to marshal failed task %s: %v", task.TaskID, err)
		return
	}

	if err := redisGateway.HandleFailedTask(ctx, taskData, task.RetryPolicy.BackoffSec); err != nil {
		logrus.Errorf("failed to handle failed task %s: %v", task.TaskID, err)
	}

	span := trace.SpanFromContext(ctx)
	span.AddEvent(errMsg)
	span.SetStatus(codes.Error, fmt.Sprintf(consts.SpanStatusDescription, task.TaskID, consts.GetTaskStateName(consts.TaskError)))
	span.End()

	logrus.WithField("task_id", task.TaskID).Errorf("failed after %d attempts", task.RetryPolicy.MaxAttempts)
}

// CancelTask cancels a task and removes it from the queues
func CancelTask(redisGateway *redisinfra.Gateway, taskID string) error {
	// Cancel execution context
	taskCancelFuncsMutex.RLock()
	cancelFunc, exists := taskCancelFuncs[taskID]
	taskCancelFuncsMutex.RUnlock()

	if exists {
		cancelFunc()
	}

	// Remove task from Redis
	ctx := consumerDetachedContext()

	// Locate queue using index
	queueType, err := redisGateway.GetTaskQueue(ctx, taskID)
	if err == nil {
		switch queueType {
		case ReadyQueueKey:
			if _, err := redisGateway.RemoveFromList(ctx, ReadyQueueKey, taskID); err != nil {
				logrus.Warnf("failed to remove from list: %v", err)
			}
		case DelayedQueueKey:
			if s := redisGateway.RemoveFromZSet(ctx, DelayedQueueKey, taskID); !s {
				logrus.Warnf("failed to remove from delayed queue: %v", err)
			}
		case DeadLetterKey:
			if s := redisGateway.RemoveFromZSet(ctx, DeadLetterKey, taskID); !s {
				logrus.Warnf("failed to remove from dead letter queue: %v", err)
			}
		}
	}

	// Clean up index
	if err := redisGateway.DeleteTaskIndex(ctx, taskID); err != nil {
		logrus.Warnf("failed to delete task index: %v", err)
	}

	if exists || err == nil {
		return nil
	}

	return fmt.Errorf("task %s not found", taskID)
}

// ===================== Redis Event Publishing =====================

// publishEvent publishes a StreamEvent to the specified Redis stream
// This adds caller information and handles error logging
func publishEvent(gateway *redisinfra.Gateway, ctx context.Context, stream string, event dto.TraceStreamEvent, opts ...eventPublishOption) {
	options := &eventPublishOptions{
		callerLevel: 2,
	}
	for _, opt := range opts {
		opt(options)
	}

	// Business logic: Enhance event with caller information
	file, line, fn := utils.GetCallerInfo(options.callerLevel)
	event.FileName = file
	event.Line = line
	event.FnName = fn

	// Call repository layer for data access
	if err := publishTraceStreamEvent(gateway, ctx, stream, &event); err != nil {
		if err == goredis.Nil {
			logrus.Warnf("No new messages to publish to Redis stream %s", stream)
			return
		}
		logrus.Errorf("Failed to publish event to Redis stream %s: %v", stream, err)
	}
}

// updateTaskState updates the task states and publishes the update
func updateTaskState(ctx context.Context, update *taskStateUpdate) {
	err := tracing.WithSpan(ctx, func(childCtx context.Context) error {
		db := update.db
		if db == nil {
			return fmt.Errorf("task state update db is nil")
		}
		if update.redisGateway == nil {
			return fmt.Errorf("task state update redis gateway is nil")
		}

		span := trace.SpanFromContext(childCtx)
		logEntry := logrus.WithField("trace_id", update.traceID).WithField("task_id", update.taskID)
		span.AddEvent(update.message)

		description := fmt.Sprintf(consts.SpanStatusDescription, update.taskID, consts.GetTaskStateName(update.taskState))
		if update.taskState == consts.TaskCompleted {
			span.SetStatus(codes.Ok, description)
		}

		if update.taskState == consts.TaskError {
			span.SetStatus(codes.Error, description)
		}

		stream := fmt.Sprintf(consts.StreamTraceLogKey, update.traceID)

		// Publish custom event or default state update event
		if update.event != nil {
			publishEvent(update.redisGateway, childCtx, stream, *update.event, withCallerLevel(5))
		}

		if err := updateTaskStateRecord(db, childCtx, update.taskID, update.taskState); err != nil {
			logEntry.Errorf("failed to update database: %v", err)
			return err
		}

		if err := updateTraceState(update.redisGateway, db, update.traceID, update.taskID, update.taskState, update.event); err != nil {
			logEntry.Errorf("failed to update trace state: %v", err)
			return err
		}

		return nil
	})

	if err != nil {
		logrus.WithField("task_id", update.taskID).Errorf("failed to update task state: %v", err)
	}
}

func updateTaskStateRecord(db *gorm.DB, ctx context.Context, taskID string, state consts.TaskState) error {
	return db.WithContext(ctx).Model(&model.Task{}).
		Where("id = ?", taskID).
		Update("state", state).Error
}
