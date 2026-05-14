package consumer

import (
	"context"
	"fmt"

	"aegis/core/domain/container"
	"aegis/core/orchestrator/common"
	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/utils"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
	batchv1 "k8s.io/api/batch/v1"
)

// jobAddDispatch is the per-task-type result of the HandleJobAdd switch:
// the message, event type, and payload that get passed to updateTaskState.
type jobAddDispatch struct {
	message   string
	eventType consts.EventType
	payload   any
}

// jobAddHandler computes the JobAdd dispatch for a given task type.
type jobAddHandler func(annot *k8sAnnotationData, labels *jobLabels, jobName string) jobAddDispatch

// jobFailedDispatch is the per-task-type result of the HandleJobFailed switch:
// the event name and payload that get passed to the trailing updateTaskState.
type jobFailedDispatch struct {
	eventName consts.EventType
	payload   any
}

// jobFailedHandler runs the per-task-type body of HandleJobFailed. The
// bool return is "continue": true means HandleJobFailed should call the
// trailing updateTaskState with the returned dispatch; false means the
// handler already terminated (typically via errCtx.Fatal) and the caller
// should return without further work.
type jobFailedHandler func(
	h *k8sHandler,
	taskCtx context.Context,
	annot *k8sAnnotationData,
	labels *jobLabels,
	job *batchv1.Job,
	errCtx *errorContext,
	logEntry *logrus.Entry,
	taskSpan trace.Span,
) (jobFailedDispatch, bool)

// jobSucceededHandler runs the per-task-type body of HandleJobSucceeded.
// Each branch is fully self-contained (updates state and submits the next
// task), so this returns nothing.
type jobSucceededHandler func(
	h *k8sHandler,
	taskCtx context.Context,
	traceCtx context.Context,
	annot *k8sAnnotationData,
	labels *jobLabels,
	job *batchv1.Job,
	errCtx *errorContext,
	logEntry *logrus.Entry,
	taskSpan trace.Span,
)

// taskHandlerSet groups the per-hook handlers for a single task type.
// A nil hook means the task type does not implement that hook; the
// caller treats this as a no-op.
type taskHandlerSet struct {
	onJobAdd       jobAddHandler
	onJobFailed    jobFailedHandler
	onJobSucceeded jobSucceededHandler
}

// taskHandlers dispatches k8s callback hooks by task type. Only task
// types that map to k8s Jobs need entries here.
var taskHandlers = map[consts.TaskType]taskHandlerSet{
	consts.TaskTypeBuildDatapack: {
		onJobAdd:       handleBuildDatapackJobAdd,
		onJobFailed:    handleBuildDatapackJobFailed,
		onJobSucceeded: handleBuildDatapackJobSucceeded,
	},
	consts.TaskTypeRunAlgorithm: {
		onJobAdd:       handleRunAlgorithmJobAdd,
		onJobFailed:    handleRunAlgorithmJobFailed,
		onJobSucceeded: handleRunAlgorithmJobSucceeded,
	},
}

// ---------- BuildDatapack ----------

func handleBuildDatapackJobAdd(annot *k8sAnnotationData, labels *jobLabels, jobName string) jobAddDispatch {
	return jobAddDispatch{
		message:   fmt.Sprintf("building dataset for task %s", labels.taskID),
		eventType: consts.EventDatapackBuildStarted,
		payload: dto.DatapackInfo{
			Datapack: annot.datapack,
			JobName:  jobName,
		},
	}
}

func handleBuildDatapackJobFailed(
	h *k8sHandler,
	taskCtx context.Context,
	annot *k8sAnnotationData,
	labels *jobLabels,
	job *batchv1.Job,
	errCtx *errorContext,
	logEntry *logrus.Entry,
	taskSpan trace.Span,
) (jobFailedDispatch, bool) {
	// Release the BuildDatapack token first so a flood of failures
	// does not wedge the bucket. Release-on-failure mirrors the
	// algorithm path below.
	if rateLimiter := h.buildDatapackLimiter; rateLimiter != nil {
		if releaseErr := rateLimiter.ReleaseToken(taskCtx, labels.taskID, labels.traceID); releaseErr != nil {
			errCtx.Warn(nil, "failed to release build datapack token on job failure", releaseErr)
		} else {
			logEntry.Info("successfully released build datapack token on job failure")
			taskSpan.AddEvent("successfully released build datapack token on job failure")
		}
	} else {
		errCtx.Warn(nil, "build datapack rate limiter not initialized on job failure", fmt.Errorf("build datapack rate limiter not initialized"))
	}

	logEntry.Error("datapack build failed")
	taskSpan.AddEvent("datapack build failed")

	dispatch := jobFailedDispatch{
		eventName: consts.EventDatapackBuildFailed,
		payload: dto.DatapackResult{
			Datapack: annot.datapack.Name,
			JobName:  job.Name,
		},
	}

	if err := h.store.updateInjectionState(taskCtx, annot.datapack.Name, consts.DatapackBuildFailed); err != nil {
		errCtx.Warn(nil, "update injection state failed", err)
	}

	return dispatch, true
}

func handleBuildDatapackJobSucceeded(
	h *k8sHandler,
	taskCtx context.Context,
	traceCtx context.Context,
	annot *k8sAnnotationData,
	labels *jobLabels,
	job *batchv1.Job,
	errCtx *errorContext,
	logEntry *logrus.Entry,
	taskSpan trace.Span,
) {
	// Release the BuildDatapack token now that the job has finished;
	// holding it any longer would slow-leak the bucket. Mirrors the
	// release-on-success of the algorithm path below.
	if rateLimiter := h.buildDatapackLimiter; rateLimiter != nil {
		if releaseErr := rateLimiter.ReleaseToken(taskCtx, labels.taskID, labels.traceID); releaseErr != nil {
			errCtx.Warn(nil, "failed to release build datapack token on job success", releaseErr)
		} else {
			logEntry.Info("successfully released build datapack token on job success")
			taskSpan.AddEvent("successfully released build datapack token on job success")
		}
	} else {
		errCtx.Warn(nil, "build datapack rate limiter not initialized on job success", fmt.Errorf("build datapack rate limiter not initialized"))
	}

	logEntry.Info("datapack build successfully")
	taskSpan.AddEvent("datapack build successfully")

	if err := h.store.updateInjectionState(taskCtx, annot.datapack.Name, consts.DatapackBuildSuccess); err != nil {
		errCtx.Fatal(nil, "update injection state failed", err)
		return
	}

	updateTaskState(taskCtx,
		newTaskStateUpdate(
			labels.traceID,
			labels.taskID,
			labels.taskType,
			consts.TaskCompleted,
			fmt.Sprintf(consts.TaskMsgCompleted, labels.taskID),
		).withEvent(
			consts.EventDatapackBuildSucceed,
			dto.DatapackResult{
				Datapack: annot.datapack.Name,
				JobName:  job.Name,
			},
		).withDB(h.db).withRedis(h.redisGateway),
	)

	ref := &dto.ContainerRef{
		Name: config.GetDetectorName(),
	}

	algorithmVersionResults, err := container.NewRepository(h.db).ResolveContainerVersions([]*dto.ContainerRef{ref}, consts.ContainerTypeAlgorithm, labels.userID)
	if err != nil {
		errCtx.Fatal(nil, "failed to map container refs to versions", err)
		return
	}
	if len(algorithmVersionResults) == 0 {
		errCtx.Fatal(nil, "no valid algorithm versions found", nil)
		return
	}

	algorithmVersion, exists := algorithmVersionResults[ref]
	if !exists {
		errCtx.Fatal(nil, "algorithm version not found for item", nil)
		return
	}

	payload := map[string]any{
		consts.ExecuteAlgorithm:        dto.NewContainerVersionItem(&algorithmVersion),
		consts.ExecuteDatapack:         annot.datapack,
		consts.ExecuteDatasetVersionID: consts.DefaultInvalidID,
	}

	task := &dto.UnifiedTask{
		Type:         consts.TaskTypeRunAlgorithm,
		Immediate:    true,
		Payload:      payload,
		ParentTaskID: utils.StringPtr(labels.taskID),
		TraceID:      labels.traceID,
		GroupID:      labels.groupID,
		ProjectID:    labels.projectID,
		UserID:       labels.userID,
	}
	task.SetTraceCtx(traceCtx)

	if err := common.SubmitTaskWithDB(taskCtx, h.db, h.redisGateway, task); err != nil {
		errCtx.Warn(nil, "submit algorithm execution task failed", err)
	}
}

// ---------- RunAlgorithm ----------

func handleRunAlgorithmJobAdd(annot *k8sAnnotationData, labels *jobLabels, jobName string) jobAddDispatch {
	return jobAddDispatch{
		message:   fmt.Sprintf("running algorithm for task %s", labels.taskID),
		eventType: consts.EventAlgoRunStarted,
		payload: dto.ExecutionInfo{
			Algorithm:   annot.algorithm,
			Datapack:    annot.datapack,
			ExecutionID: *labels.ExecutionID,
			JobName:     jobName,
		},
	}
}

func handleRunAlgorithmJobFailed(
	h *k8sHandler,
	taskCtx context.Context,
	annot *k8sAnnotationData,
	labels *jobLabels,
	job *batchv1.Job,
	errCtx *errorContext,
	logEntry *logrus.Entry,
	taskSpan trace.Span,
) (jobFailedDispatch, bool) {
	rateLimiter := h.algoLimiter
	if rateLimiter == nil {
		errCtx.Warn(nil, "algorithm execution rate limiter not initialized on job failure", fmt.Errorf("algorithm execution rate limiter not initialized"))
		return jobFailedDispatch{}, false
	}
	if releaseErr := rateLimiter.ReleaseToken(taskCtx, labels.taskID, labels.traceID); releaseErr != nil {
		errCtx.Warn(nil, "failed to release algorithm execution token on job failure", releaseErr)
	} else {
		logEntry.Info("successfully released algorithm execution token on job failure")
		taskSpan.AddEvent("successfully released algorithm execution token on job failure")
	}

	if annot.algorithm == nil {
		errCtx.Fatal(nil, "missing algorithm information in annotations", nil)
		return jobFailedDispatch{}, false
	}
	if labels.ExecutionID == nil {
		errCtx.Fatal(nil, "missing execution ID in job labels", nil)
		return jobFailedDispatch{}, false
	}

	logEntry.Error("algorithm execute failed")
	taskSpan.AddEvent("algorithm execute failed")

	dispatch := jobFailedDispatch{
		eventName: consts.EventAlgoRunFailed,
		payload: dto.ExecutionResult{
			Algorithm: annot.algorithm.ContainerName,
			JobName:   job.Name,
		},
	}

	if annot.algorithm.ContainerName == config.GetDetectorName() {
		if err := h.store.updateInjectionState(taskCtx, annot.datapack.Name, consts.DatapackDetectorFailed); err != nil {
			errCtx.Warn(nil, "update injection state failed", err)
		}
	}

	if err := h.store.updateExecutionState(taskCtx, *labels.ExecutionID, consts.ExecutionFailed); err != nil {
		errCtx.Fatal(nil, "update execution state failed", err)
		return jobFailedDispatch{}, false
	}

	return dispatch, true
}

func handleRunAlgorithmJobSucceeded(
	h *k8sHandler,
	taskCtx context.Context,
	traceCtx context.Context,
	annot *k8sAnnotationData,
	labels *jobLabels,
	job *batchv1.Job,
	errCtx *errorContext,
	logEntry *logrus.Entry,
	taskSpan trace.Span,
) {
	rateLimiter := h.algoLimiter
	if rateLimiter == nil {
		errCtx.Warn(nil, "algorithm execution rate limiter not initialized on job success", fmt.Errorf("algorithm execution rate limiter not initialized"))
		return
	}
	if releaseErr := rateLimiter.ReleaseToken(taskCtx, labels.taskID, labels.traceID); releaseErr != nil {
		errCtx.Warn(nil, "failed to release algorithm execution token on job success", releaseErr)
	} else {
		logEntry.Info("successfully released algorithm execution token on job success")
		taskSpan.AddEvent("successfully released algorithm execution token on job success")
	}

	if annot.algorithm == nil {
		errCtx.Fatal(nil, "missing algorithm information in annotations", nil)
		return
	}

	if labels.ExecutionID == nil {
		errCtx.Fatal(nil, "missing execution ID in job labels", nil)
		return
	}

	logEntry.Info("algorithm execute successfully")
	taskSpan.AddEvent("algorithm execute successfully")

	if annot.algorithm.ContainerName == config.GetDetectorName() {
		if err := h.store.updateInjectionState(taskCtx, annot.datapack.Name, consts.DatapackDetectorSuccess); err != nil {
			errCtx.Fatal(nil, "update injection state failed", err)
			return
		}
	}

	if err := h.store.updateExecutionState(taskCtx, *labels.ExecutionID, consts.ExecutionSuccess); err != nil {
		errCtx.Fatal(nil, "update execution state failed", err)
		return
	}

	updateTaskState(taskCtx,
		newTaskStateUpdate(
			labels.traceID,
			labels.taskID,
			labels.taskType,
			consts.TaskCompleted,
			fmt.Sprintf(consts.TaskMsgCompleted, labels.taskID),
		).withEvent(
			consts.EventAlgoRunSucceed,
			dto.ExecutionResult{
				Algorithm: annot.algorithm.ContainerName,
				JobName:   job.Name,
			},
		).withDB(h.db).withRedis(h.redisGateway),
	)

	payload := map[string]any{
		consts.CollectAlgorithm:   annot.algorithm,
		consts.CollectDatapack:    annot.datapack,
		consts.CollectExecutionID: *labels.ExecutionID,
	}

	task := &dto.UnifiedTask{
		Type:         consts.TaskTypeCollectResult,
		Immediate:    true,
		Payload:      payload,
		ParentTaskID: utils.StringPtr(labels.taskID),
		TraceID:      labels.traceID,
		GroupID:      labels.groupID,
		ProjectID:    labels.projectID,
		UserID:       labels.userID,
	}
	task.SetTraceCtx(traceCtx)

	if err := common.SubmitTaskWithDB(taskCtx, h.db, h.redisGateway, task); err != nil {
		errCtx.Warn(nil, "submit result collection task failed", err)
	}
}
