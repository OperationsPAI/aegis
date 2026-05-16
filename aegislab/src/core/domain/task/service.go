package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"
	k8sinfra "aegis/platform/k8s"
	"aegis/platform/model"
	redisinfra "aegis/platform/redis"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type Service struct {
	repository *Repository
	logService *TaskLogService
	loki       *LokiGateway
	redis      *redisinfra.Gateway
	k8s        *k8sinfra.Gateway
}

func NewService(repository *Repository, logService *TaskLogService, loki *LokiGateway, redis *redisinfra.Gateway, k8s *k8sinfra.Gateway) *Service {
	return &Service{
		repository: repository,
		logService: logService,
		loki:       loki,
		redis:      redis,
		k8s:        k8s,
	}
}

func (s *Service) BatchDelete(ctx context.Context, taskIDs []string) error {
	if len(taskIDs) == 0 {
		return nil
	}

	return s.repository.BatchDelete(taskIDs)
}

func (s *Service) GetDetail(ctx context.Context, taskID string) (*dto.TaskDetailResp, error) {
	task, err := s.repository.GetByID(taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: task id: %s", consts.ErrNotFound, taskID)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	logs := s.queryHistoricalLogs(ctx, task)
	return dto.NewTaskDetailResp(task, logs), nil
}

func (s *Service) List(ctx context.Context, req *ListTaskReq) (*dto.ListResp[dto.TaskResp], error) {
	if req == nil {
		return nil, fmt.Errorf("list tasks request is nil")
	}

	limit, offset := req.ToGormParams()
	filterOptions := req.ToFilterOptions()

	tasks, total, err := s.repository.List(limit, offset, filterOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	taskResps := make([]dto.TaskResp, 0, len(tasks))
	for _, task := range tasks {
		taskResps = append(taskResps, *dto.NewTaskResp(&task))
	}

	return &dto.ListResp[dto.TaskResp]{
		Items:      taskResps,
		Pagination: req.ConvertToPaginationInfo(total),
	}, nil
}

// Expedite moves a Pending task's execute_time to now.
// Contract:
//   - task not found → wrapped consts.ErrNotFound
//   - task not Pending → wrapped consts.ErrBadRequest
//   - already due → no-op, returns task resp (idempotent)
//
// DB update is authoritative; Redis rescore is best-effort — if the entry
// is already promoted by the scheduler, the call still succeeds.
func (s *Service) Expedite(ctx context.Context, taskID string) (*dto.TaskResp, error) {
	task, err := s.repository.GetByID(taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: task id: %s", consts.ErrNotFound, taskID)
		}
		return nil, fmt.Errorf("failed to load task: %w", err)
	}

	if task.State != consts.TaskPending {
		return nil, fmt.Errorf("%w: state=%s, cannot expedite",
			consts.ErrBadRequest, consts.GetTaskStateName(task.State))
	}

	now := time.Now().Unix()
	if task.ExecuteTime <= now {
		return dto.NewTaskResp(task), nil
	}

	if err := s.repository.UpdateExecuteTime(taskID, now); err != nil {
		return nil, fmt.Errorf("failed to update execute_time: %w", err)
	}

	if _, err := s.redis.ExpediteDelayedTask(ctx, taskID, now); err != nil {
		logrus.WithField("task_id", taskID).
			Warnf("DB updated but Redis rescore failed: %v", err)
	}

	s.emitExpediteScheduledEvent(ctx, task, now)

	task.ExecuteTime = now
	return dto.NewTaskResp(task), nil
}

// emitExpediteScheduledEvent publishes a task.scheduled event for a manually
// expedited task. Best-effort — failures are logged only.
func (s *Service) emitExpediteScheduledEvent(ctx context.Context, task *model.Task, executeTime int64) {
	if task == nil || task.TraceID == "" || s.redis == nil {
		return
	}
	event := dto.TraceStreamEvent{
		TaskID:    task.ID,
		TaskType:  task.Type,
		EventName: consts.EventTaskScheduled,
		Payload: dto.TaskScheduledPayload{
			ExecuteTime: executeTime,
			Reason:      dto.TaskScheduledReasonExpedite,
		},
	}
	stream := fmt.Sprintf(consts.StreamTraceLogKey, task.TraceID)
	if err := s.redis.XAdd(ctx, stream, event.ToRedisStream()); err != nil {
		logrus.WithField("task_id", task.ID).
			Warnf("failed to emit expedite task.scheduled event: %v", err)
	}
}

// CancelTask marks a non-terminal task as Cancelled and best-effort evicts
// its redis queue entries + cluster-side PodChaos CRDs labelled with
// task_id=<id>. Mirrors trace.Service.CancelTrace but scoped to a single
// task — used by the UI when only one execution/injection needs to stop.
//
// Contract:
//   - task not found → wrapped consts.ErrNotFound
//   - task already terminal (Completed/Error/Cancelled) → no-op response
//     with state set to the current terminal state, no error
//   - otherwise: DB state → Cancelled, redis queue entries best-effort
//     evicted, PodChaos with label task_id=<id> best-effort deleted
func (s *Service) CancelTask(ctx context.Context, taskID string) (*CancelTaskResp, error) {
	task, err := s.repository.GetByID(taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: task id: %s", consts.ErrNotFound, taskID)
		}
		return nil, fmt.Errorf("failed to load task: %w", err)
	}

	logEntry := logrus.WithField("task_id", taskID)

	switch task.State {
	case consts.TaskCompleted, consts.TaskError, consts.TaskCancelled:
		return &CancelTaskResp{
			TaskID: taskID,
			State:  consts.GetTaskStateName(task.State),
			Message: fmt.Sprintf("task already terminal (%s); nothing to cancel",
				consts.GetTaskStateName(task.State)),
		}, nil
	}

	if err := s.repository.MarkCancelled(taskID); err != nil {
		return nil, fmt.Errorf("failed to mark task cancelled: %w", err)
	}

	resp := &CancelTaskResp{
		TaskID: taskID,
		State:  consts.GetTaskStateName(consts.TaskCancelled),
	}

	if s.redis != nil {
		removed := false
		if ok := s.redis.RemoveFromZSet(ctx, redisinfra.DelayedQueueKey, taskID); ok {
			removed = true
		}
		if ok, err := s.redis.RemoveFromList(ctx, redisinfra.ReadyQueueKey, taskID); err == nil && ok {
			removed = true
		} else if err != nil {
			logEntry.Warnf("failed to remove task from ready queue: %v", err)
		}
		if ok := s.redis.RemoveFromZSet(ctx, redisinfra.DeadLetterKey, taskID); ok {
			removed = true
		}
		if err := s.redis.DeleteTaskIndex(ctx, taskID); err != nil {
			logEntry.Warnf("failed to clear task index: %v", err)
		}
		if removed {
			resp.RemovedRedisTasks = append(resp.RemovedRedisTasks, taskID)
		}
	}

	if s.k8s != nil {
		deleted, warnings := s.k8s.DeleteChaosCRDsByLabel(ctx, consts.JobLabelTaskID, taskID)
		for _, d := range deleted {
			resp.DeletedPodChaos = append(resp.DeletedPodChaos, d.Name)
		}
		for _, w := range warnings {
			logEntry.Warnf("chaos CRD cleanup warning: %v", w)
		}
	}

	resp.Message = fmt.Sprintf("cancelled task %s (podchaos=%d, redis_evicted=%d)",
		taskID, len(resp.DeletedPodChaos), len(resp.RemovedRedisTasks))
	return resp, nil
}

func (s *Service) GetForLogStream(ctx context.Context, taskID string) (*model.Task, error) {
	task, err := s.repository.GetByID(taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: task id: %s", consts.ErrNotFound, taskID)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	return task, nil
}

func (s *Service) StreamLogs(ctx context.Context, conn *websocket.Conn, task *model.Task) {
	s.logService.StreamLogs(ctx, conn, task)
}

func (s *Service) PollLogs(ctx context.Context, taskID string, after time.Time) (*TaskLogPollResp, error) {
	task, err := s.repository.GetByID(taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: task id: %s", consts.ErrNotFound, taskID)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	start := task.CreatedAt
	if !after.IsZero() && after.After(start) {
		start = after.Add(time.Nanosecond)
	}

	lokiCtx, lokiCancel := context.WithTimeout(ctx, 10*time.Second)
	defer lokiCancel()

	logEntries, err := s.loki.QueryJobLogs(lokiCtx, task.ID, start)
	if err != nil {
		return nil, fmt.Errorf("failed to query task logs: %w", err)
	}

	return &TaskLogPollResp{
		Logs:      logEntries,
		Terminal:  isTaskTerminal(task.State),
		State:     consts.GetTaskStateName(task.State),
		CreatedAt: task.CreatedAt,
	}, nil
}

func (s *Service) queryHistoricalLogs(ctx context.Context, task *model.Task) []string {
	lokiCtx, lokiCancel := context.WithTimeout(ctx, 10*time.Second)
	defer lokiCancel()

	logEntries, err := s.loki.QueryJobLogs(lokiCtx, task.ID, task.CreatedAt)
	if err != nil {
		logrus.Warnf("Failed to query Loki for task %s logs: %v", task.ID, err)
		return []string{}
	}

	logs := make([]string, 0, len(logEntries))
	for _, entry := range logEntries {
		logs = append(logs, entry.Line)
	}
	return logs
}
