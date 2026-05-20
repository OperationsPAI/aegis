package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"
	k8s "aegis/platform/k8s"
	redis "aegis/platform/redis"
	"aegis/platform/tracing"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
	batchv1 "k8s.io/api/batch/v1"
)

const (
	crdLabelsErrMsg = "missing or invalid '%s' key in k8s CRD labels"
	jobLabelsErrMsg = "missing or invalid '%s' key in k8s job labels"
)

// errorContext holds common context for error handling
type errorContext struct {
	ctx          context.Context
	span         trace.Span
	logEntry     *logrus.Entry
	labels       *taskIdentifiers
	db           *gorm.DB
	redisGateway *redis.Gateway
}

// NewErrorContext creates an ErrorContext from parsed labels
func NewErrorContext(ctx context.Context, db *gorm.DB, redisGateway *redis.Gateway, span trace.Span, labels *taskIdentifiers) *errorContext {
	return &errorContext{
		ctx:  ctx,
		span: span,
		logEntry: logrus.WithFields(logrus.Fields{
			"task_id":  labels.taskID,
			"trace_id": labels.traceID,
		}),
		labels:       labels,
		db:           db,
		redisGateway: redisGateway,
	}
}

// Fatal handles a fatal error and updates task state
func (e *errorContext) Fatal(logEntry *logrus.Entry, message string, err error) {
	if logEntry == nil {
		logEntry = e.logEntry
	}

	if err != nil {
		logEntry.Errorf("%s: %v", message, err)
		e.span.RecordError(err)
	} else {
		logEntry.Error(message)
	}

	e.span.AddEvent(message)

	updateTaskState(e.ctx,
		newTaskStateUpdate(
			e.labels.traceID,
			e.labels.taskID,
			e.labels.taskType,
			consts.TaskError,
			message,
		).withDB(e.db).withRedis(e.redisGateway),
	)
}

// Warn handles a tolerable error without updating task state
func (e *errorContext) Warn(logEntry *logrus.Entry, message string, err error) {
	if logEntry == nil {
		logEntry = e.logEntry
	}

	if err != nil {
		logEntry.Warnf("%s (non-fatal): %v", message, err)
		e.span.RecordError(err)
	} else {
		logEntry.Warn(message)
	}

	e.span.AddEvent(fmt.Sprintf("%s (continuing)", message))
}

type k8sAnnotationData struct {
	taskCarrier      propagation.MapCarrier
	traceCarrier     propagation.MapCarrier
	rootTraceCarrier propagation.MapCarrier

	algorithm *dto.ContainerVersionItem
	benchmark *dto.ContainerVersionItem
	datapack  *dto.InjectionItem
}

type taskIdentifiers struct {
	taskID    string
	taskType  consts.TaskType
	traceID   string
	groupID   string
	projectID int
	userID    int
}

type crdLabels struct {
	taskIdentifiers
	batchID  string
	IsHybrid bool
}

type jobLabels struct {
	taskIdentifiers
	ExecutionID *int
}

type k8sHandler struct {
	db                   *gorm.DB
	store                *stateStore
	monitor              NamespaceMonitor
	algoLimiter          *TokenBucketRateLimiter
	buildDatapackLimiter *TokenBucketRateLimiter
	k8sGateway           *k8s.Gateway
	redisGateway         *redis.Gateway
	batchManager         *FaultBatchManager
}

func NewHandler(db *gorm.DB, monitor NamespaceMonitor, algoLimiter *TokenBucketRateLimiter, buildDatapackLimiter *TokenBucketRateLimiter, k8sGateway *k8s.Gateway, redisGateway *redis.Gateway, batchManager *FaultBatchManager, execution ExecutionOwner, injection InjectionOwner) *k8sHandler {
	return &k8sHandler{
		db:                   db,
		store:                newStateStore(execution, injection),
		monitor:              monitor,
		algoLimiter:          algoLimiter,
		buildDatapackLimiter: buildDatapackLimiter,
		k8sGateway:           k8sGateway,
		redisGateway:         redisGateway,
		batchManager:         batchManager,
	}
}


func (h *k8sHandler) HandleJobAdd(name string, annotations map[string]string, labels map[string]string) {
	parsedAnnotations, err := parseAnnotations(annotations)
	if err != nil {
		logrus.Errorf("HandleJobAdd: failed to parse annotations: %v", err)
		return
	}

	parsedLabels, err := parseJobLabels(labels)
	if err != nil {
		logrus.Errorf("HandleJobAdd: failed to parse job labels: %v", err)
		return
	}

	handlerSet, ok := taskHandlers[parsedLabels.taskType]
	if !ok || handlerSet.onJobAdd == nil {
		return
	}
	dispatch := handlerSet.onJobAdd(parsedAnnotations, parsedLabels, name)

	taskCtx := otel.GetTextMapPropagator().Extract(consumerDetachedContext(), parsedAnnotations.taskCarrier)
	_ = tracing.WithSpanNamed(taskCtx, "k8s/callback/JobAdd", func(ctx context.Context) error {
		tracing.SetSpanAttribute(ctx, "task.id", parsedLabels.taskID)
		tracing.SetSpanAttribute(ctx, "task.type", consts.GetTaskTypeName(parsedLabels.taskType))
		tracing.SetSpanAttribute(ctx, "job.name", name)
		updateTaskState(ctx,
			newTaskStateUpdate(
				parsedLabels.traceID,
				parsedLabels.taskID,
				parsedLabels.taskType,
				consts.TaskRunning,
				dispatch.message,
			).withEvent(dispatch.eventType, dispatch.payload).withDB(h.db).withRedis(h.redisGateway),
		)
		return nil
	})
}

func (h *k8sHandler) HandleJobFailed(job *batchv1.Job, annotations map[string]string, labels map[string]string) {
	parsedAnnotations, err := parseAnnotations(annotations)
	if err != nil {
		logrus.Errorf("HandleJobFailed: parse annotations failed: %v", err)
		return
	}

	parsedLabels, err := parseJobLabels(labels)
	if err != nil {
		logrus.Errorf("HandleJobFailed: parse job labels failed: %v", err)
		return
	}

	logEntry := logrus.WithFields(logrus.Fields{
		"task_id":  parsedLabels.taskID,
		"trace_id": parsedLabels.traceID,
	})
	taskCtx := otel.GetTextMapPropagator().Extract(consumerDetachedContext(), parsedAnnotations.taskCarrier)
	_ = tracing.WithSpanNamed(taskCtx, "k8s/callback/JobFailed", func(taskCtx context.Context) error {
	emitK8sDispatchGap(taskCtx, "Job", job.Name, job.CreationTimestamp.Time)
	tracing.SetSpanAttribute(taskCtx, "task.id", parsedLabels.taskID)
	tracing.SetSpanAttribute(taskCtx, "task.type", consts.GetTaskTypeName(parsedLabels.taskType))
	tracing.SetSpanAttribute(taskCtx, "job.name", job.Name)
	tracing.SetSpanAttribute(taskCtx, "job.namespace", job.Namespace)
	taskSpan := trace.SpanFromContext(taskCtx)

	errCtx := NewErrorContext(taskCtx, h.db, h.redisGateway, taskSpan, &parsedLabels.taskIdentifiers)

	if parsedAnnotations.datapack == nil {
		errCtx.Fatal(nil, "missing datapack information in annotations", nil)
		return nil
	}

	if h.k8sGateway == nil {
		errCtx.Warn(nil, "k8s gateway not initialized", fmt.Errorf("k8s gateway not initialized"))
		return nil
	}
	logMap, err := h.k8sGateway.GetJobPodLogs(taskCtx, job.Namespace, job.Name)
	if err != nil {
		errCtx.Warn(logrus.WithField("job_name", job.Name), "failed to get job logs", err)
	}

	var filePath string
	if len(logMap) > 0 {
		spanAttrs := []trace.EventOption{
			trace.WithAttributes(
				attribute.String("job_name", job.Name),
				attribute.String("namespace", job.Namespace),
			),
		}
		taskSpan.AddEvent("job failed", spanAttrs...)
	}

	publishEvent(h.redisGateway, taskCtx, fmt.Sprintf(consts.StreamTraceLogKey, parsedLabels.traceID), dto.TraceStreamEvent{
		TaskID:    parsedLabels.taskID,
		TaskType:  parsedLabels.taskType,
		EventName: consts.EventJobFailed,
		Payload: dto.JobMessage{
			JobName:   job.Name,
			Namespace: job.Namespace,
			LogFile:   filePath,
		},
	}, withCallerLevel(4))

	handlerSet, ok := taskHandlers[parsedLabels.taskType]
	if !ok || handlerSet.onJobFailed == nil {
		return nil
	}
	dispatch, cont := handlerSet.onJobFailed(h, taskCtx, parsedAnnotations, parsedLabels, job, errCtx, logEntry, taskSpan)
	if !cont {
		return nil
	}

	updateTaskState(taskCtx,
		newTaskStateUpdate(
			parsedLabels.traceID,
			parsedLabels.taskID,
			parsedLabels.taskType,
			consts.TaskError,
			fmt.Sprintf(consts.TaskMsgFailed, parsedLabels.taskID),
		).withEvent(dispatch.eventName, dispatch.payload).withDB(h.db).withRedis(h.redisGateway),
	)
	return nil
	})
}

func (h *k8sHandler) HandleJobSucceeded(job *batchv1.Job, annotations map[string]string, labels map[string]string) {
	parsedAnnotations, err := parseAnnotations(annotations)
	if err != nil {
		logrus.Errorf("HandleJobSucceeded: failed to parse annotations: %v", err)
		return
	}

	parsedLabels, err := parseJobLabels(labels)
	if err != nil {
		logrus.Errorf("HandleJobSucceeded: failed to parse job labels: %v", err)
		return
	}

	stream := fmt.Sprintf(consts.StreamTraceLogKey, parsedLabels.traceID)

	taskCtx := otel.GetTextMapPropagator().Extract(consumerDetachedContext(), parsedAnnotations.taskCarrier)
	traceCtx := otel.GetTextMapPropagator().Extract(consumerDetachedContext(), parsedAnnotations.traceCarrier)

	_ = tracing.WithSpanNamed(taskCtx, "k8s/callback/JobSucceeded", func(taskCtx context.Context) error {
	emitK8sDispatchGap(taskCtx, "Job", job.Name, job.CreationTimestamp.Time)
	tracing.SetSpanAttribute(taskCtx, "task.id", parsedLabels.taskID)
	tracing.SetSpanAttribute(taskCtx, "task.type", consts.GetTaskTypeName(parsedLabels.taskType))
	tracing.SetSpanAttribute(taskCtx, "job.name", job.Name)
	tracing.SetSpanAttribute(taskCtx, "job.namespace", job.Namespace)

	logEntry := logrus.WithFields(logrus.Fields{
		"task_id":  parsedLabels.taskID,
		"trace_id": parsedLabels.traceID,
	})
	taskSpan := trace.SpanFromContext(taskCtx)

	errCtx := NewErrorContext(taskCtx, h.db, h.redisGateway, taskSpan, &parsedLabels.taskIdentifiers)

	if parsedAnnotations.datapack == nil {
		errCtx.Fatal(nil, "missing datapack information in annotations", nil)
		return nil
	}

	publishEvent(h.redisGateway, taskCtx, stream, dto.TraceStreamEvent{
		TaskID:    parsedLabels.taskID,
		TaskType:  parsedLabels.taskType,
		EventName: consts.EventJobSucceed,
		Payload: dto.JobMessage{
			JobName:   job.Name,
			Namespace: job.Namespace,
		},
	}, withCallerLevel(4))

	handlerSet, ok := taskHandlers[parsedLabels.taskType]
	if !ok || handlerSet.onJobSucceeded == nil {
		return nil
	}
	handlerSet.onJobSucceeded(h, taskCtx, traceCtx, parsedAnnotations, parsedLabels, job, errCtx, logEntry, taskSpan)
	return nil
	})
}

func parseAnnotations(annotations map[string]string) (*k8sAnnotationData, error) {
	message := "missing or invalid '%s' key in k8s annotations"

	taskCarrierStr, ok := annotations[consts.TaskCarrier]
	if !ok {
		return nil, fmt.Errorf(message, consts.TaskCarrier)
	}

	var taskCarrier propagation.MapCarrier
	if err := json.Unmarshal([]byte(taskCarrierStr), &taskCarrier); err != nil {
		return nil, fmt.Errorf(message, consts.TaskCarrier)
	}

	traceCarrierStr, ok := annotations[consts.TraceCarrier]
	if !ok {
		return nil, fmt.Errorf(message, consts.TraceCarrier)
	}

	var traceCarrier propagation.MapCarrier
	if err := json.Unmarshal([]byte(traceCarrierStr), &traceCarrier); err != nil {
		return nil, fmt.Errorf(message, consts.TraceCarrier)
	}

	data := &k8sAnnotationData{
		taskCarrier:  taskCarrier,
		traceCarrier: traceCarrier,
	}

	if rootCarrierStr, ok := annotations[consts.RootTraceCarrier]; ok {
		var rootCarrier propagation.MapCarrier
		if err := json.Unmarshal([]byte(rootCarrierStr), &rootCarrier); err != nil {
			return nil, fmt.Errorf("failed to unmarshal '%s': %w", consts.RootTraceCarrier, err)
		}
		data.rootTraceCarrier = rootCarrier
	}

	if itemJson, exists := annotations[consts.CRDAnnotationBenchmark]; exists {
		var benchmark dto.ContainerVersionItem
		if err := json.Unmarshal([]byte(itemJson), &benchmark); err != nil {
			return nil, fmt.Errorf("failed to unmarshal '%s' to ContainerVersionItem: %w", consts.CRDAnnotationBenchmark, err)
		}
		data.benchmark = &benchmark
	}

	if itemJson, exists := annotations[consts.JobAnnotationAlgorithm]; exists {
		var algorithm dto.ContainerVersionItem
		if err := json.Unmarshal([]byte(itemJson), &algorithm); err != nil {
			return nil, fmt.Errorf("failed to unmarshal '%s' to ContainerVersionItem: %w", consts.JobAnnotationAlgorithm, err)
		}
		data.algorithm = &algorithm
	}

	if itemJson, exists := annotations[consts.JobAnnotationDatapack]; exists {
		var datapack dto.InjectionItem
		if err := json.Unmarshal([]byte(itemJson), &datapack); err != nil {
			return nil, fmt.Errorf("failed to unmarshal '%s' to ContainerVersionItem: %w", consts.JobAnnotationDatapack, err)
		}
		data.datapack = &datapack
	}

	return data, nil
}

func parseTaskIdentifiers(message string, labels map[string]string) (*taskIdentifiers, error) {
	taskID, ok := labels[consts.JobLabelTaskID]
	if !ok || taskID == "" {
		return nil, fmt.Errorf(message, consts.JobLabelTaskID)
	}

	taskTypeStr, ok := labels[consts.JobLabelTaskType]
	if !ok || taskTypeStr == "" {
		return nil, fmt.Errorf(message, consts.JobLabelTaskType)
	}
	taskType := consts.GetTaskTypeByName(taskTypeStr)
	if taskType == nil {
		return nil, fmt.Errorf(message, consts.JobLabelTaskType)
	}

	traceID, ok := labels[consts.JobLabelTraceID]
	if !ok || traceID == "" {
		return nil, fmt.Errorf(message, consts.JobLabelTraceID)
	}

	groupID, ok := labels[consts.JobLabelGroupID]
	if !ok || groupID == "" {
		return nil, fmt.Errorf(message, consts.JobLabelGroupID)
	}

	projectIDStr, ok := labels[consts.JobLabelProjectID]
	if !ok || projectIDStr == "" {
		return nil, fmt.Errorf(message, consts.JobLabelGroupID)
	}
	projectID, err := strconv.Atoi(projectIDStr)
	if err != nil {
		return nil, fmt.Errorf(message, consts.JobLabelProjectID)
	}

	userIDStr, ok := labels[consts.JobLabelUserID]
	if !ok || userIDStr == "" {
		return nil, fmt.Errorf(message, consts.JobLabelUserID)
	}
	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		return nil, fmt.Errorf(message, consts.JobLabelUserID)
	}

	return &taskIdentifiers{
		taskID:    taskID,
		taskType:  *taskType,
		traceID:   traceID,
		groupID:   groupID,
		projectID: projectID,
		userID:    userID,
	}, nil
}

func parseCRDLabels(labels map[string]string) (*crdLabels, error) {
	identifiers, err := parseTaskIdentifiers(crdLabelsErrMsg, labels)
	if err != nil {
		return nil, fmt.Errorf("failed to parse task identifiers: %w", err)
	}

	batchID, ok := labels[consts.CRDLabelBatchID]
	if !ok && batchID == "" {
		return nil, fmt.Errorf(crdLabelsErrMsg, consts.CRDLabelBatchID)
	}

	isHybridStr, ok := labels[consts.CRDLabelIsHybrid]
	if !ok || isHybridStr == "" {
		return nil, fmt.Errorf(crdLabelsErrMsg, consts.CRDLabelIsHybrid)
	}
	isHybrid, err := strconv.ParseBool(isHybridStr)
	if err != nil {
		return nil, fmt.Errorf(crdLabelsErrMsg, consts.CRDLabelIsHybrid)
	}

	return &crdLabels{
		taskIdentifiers: *identifiers,
		batchID:         batchID,
		IsHybrid:        isHybrid,
	}, nil
}

// emitK8sDispatchGap emits a backdated child span covering the wall-clock
// gap between when the K8s object was created and when the callback fired.
// Visualizes "we created a chaos CRD / Job and waited for it" — a stretch
// of trace time the orchestrator otherwise has zero spans for.
func emitK8sDispatchGap(ctx context.Context, kind, name string, creationTs time.Time) {
	if creationTs.IsZero() {
		return
	}
	_, span := otel.Tracer("rcabench/task").Start(ctx,
		"k8s.dispatch_wait/"+kind,
		trace.WithTimestamp(creationTs),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("k8s.kind", kind),
			attribute.String("k8s.name", name),
		),
	)
	span.End()
}

func parseJobLabels(labels map[string]string) (*jobLabels, error) {
	identifiers, err := parseTaskIdentifiers(jobLabelsErrMsg, labels)
	if err != nil {
		return nil, fmt.Errorf("failed to parse task identifiers: %w", err)
	}

	data := &jobLabels{
		taskIdentifiers: *identifiers,
	}

	if executionIDStr, exists := labels[consts.JobLabelExecutionID]; exists {
		executionID, err := strconv.Atoi(executionIDStr)
		if err != nil {
			return nil, fmt.Errorf(jobLabelsErrMsg, consts.JobLabelExecutionID)
		}
		data.ExecutionID = &executionID
	}

	return data, nil
}
