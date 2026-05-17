package trace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/dto"
	k8sinfra "aegis/platform/k8s"
	redisinfra "aegis/platform/redis"

	goredis "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type Service struct {
	repo  *Repository
	redis *redisinfra.Gateway
	k8s   *k8sinfra.Gateway
	spans SpanReader
}

func NewService(repo *Repository, redis *redisinfra.Gateway, k8s *k8sinfra.Gateway, spans SpanReader) *Service {
	return &Service{repo: repo, redis: redis, k8s: k8s, spans: spans}
}

// CancelTrace marks a trace as Cancelled and performs best-effort cleanup of
// its queued redis tasks and cluster-side PodChaos CRDs. The returned DTO is
// always non-nil on success — partial failures surface as warnings embedded
// in the Message / logged — so the caller (handler) can render a useful
// response even when k8s was half-way through reconciling.
//
// Contract:
//   - trace not found → wrapped consts.ErrNotFound
//   - trace already terminal (Completed/Failed/Cancelled) → no-op response
//     with state set to the current terminal state
//   - otherwise: DB state transitions atomically to Cancelled, redis queue
//     entries are best-effort evicted, and PodChaos deletion is issued.
func (s *Service) CancelTrace(ctx context.Context, traceID string) (*CancelTraceResp, error) {
	trace, err := s.repo.GetTraceByID(traceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: trace id: %s", consts.ErrNotFound, traceID)
		}
		return nil, fmt.Errorf("failed to load trace: %w", err)
	}

	logEntry := logrus.WithField("trace_id", traceID)

	// No-op if already terminal.
	switch trace.State {
	case consts.TraceCompleted, consts.TraceFailed, consts.TraceCancelled:
		return &CancelTraceResp{
			TraceID: traceID,
			State:   consts.GetTraceStateName(trace.State),
			Message: fmt.Sprintf("trace already terminal (%s); nothing to cancel",
				consts.GetTraceStateName(trace.State)),
		}, nil
	}

	taskIDs, err := s.repo.ListInFlightTaskIDsByTrace(traceID)
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate in-flight tasks: %w", err)
	}

	// 1. Transition DB state first — subsequent best-effort cleanup is
	// idempotent and doesn't invalidate the authoritative state change.
	if err := s.repo.MarkTraceCancelled(traceID); err != nil {
		return nil, fmt.Errorf("failed to mark trace cancelled: %w", err)
	}

	resp := &CancelTraceResp{
		TraceID: traceID,
		State:   consts.GetTraceStateName(consts.TraceCancelled),
	}

	// 2. Best-effort redis queue cleanup for each in-flight task.
	if s.redis != nil {
		for _, taskID := range taskIDs {
			removed := false
			if ok := s.redis.RemoveFromZSet(ctx, redisinfra.DelayedQueueKey, taskID); ok {
				removed = true
			}
			if ok, err := s.redis.RemoveFromList(ctx, redisinfra.ReadyQueueKey, taskID); err == nil && ok {
				removed = true
			} else if err != nil {
				logEntry.WithField("task_id", taskID).Warnf("failed to remove task from ready queue: %v", err)
			}
			if ok := s.redis.RemoveFromZSet(ctx, redisinfra.DeadLetterKey, taskID); ok {
				removed = true
			}
			if err := s.redis.DeleteTaskIndex(ctx, taskID); err != nil {
				logEntry.WithField("task_id", taskID).Warnf("failed to clear task index: %v", err)
			}
			resp.CancelledTasks = append(resp.CancelledTasks, taskID)
			if removed {
				resp.RemovedRedisTasks = append(resp.RemovedRedisTasks, taskID)
			}
		}
	}

	// 3. Best-effort PodChaos deletion via label selector traceID=<id>.
	if s.k8s != nil {
		deleted, warnings := s.k8s.DeleteChaosCRDsByLabel(ctx, consts.JobLabelTraceID, traceID)
		for _, d := range deleted {
			resp.DeletedPodChaos = append(resp.DeletedPodChaos, d.Name)
		}
		for _, w := range warnings {
			logEntry.Warnf("chaos CRD cleanup warning: %v", w)
		}
	}

	// 4. Publish a terminal cancellation event so SSE watchers wake up.
	s.emitCancelledEvent(ctx, traceID)

	resp.Message = fmt.Sprintf("cancelled trace %s (tasks=%d, podchaos=%d, redis_evicted=%d)",
		traceID, len(resp.CancelledTasks), len(resp.DeletedPodChaos), len(resp.RemovedRedisTasks))

	return resp, nil
}

func (s *Service) emitCancelledEvent(ctx context.Context, traceID string) {
	if s.redis == nil {
		return
	}
	evt := dto.TraceStreamEvent{
		EventName: consts.EventTraceCancelled,
		Payload: map[string]any{
			"trace_id": traceID,
			"state":    consts.GetTraceStateName(consts.TraceCancelled),
		},
	}
	stream := fmt.Sprintf(consts.StreamTraceLogKey, traceID)
	if err := s.redis.XAdd(ctx, stream, evt.ToRedisStream()); err != nil {
		logrus.WithField("trace_id", traceID).Warnf("failed to emit trace.cancelled event: %v", err)
	}
}

func (s *Service) GetTrace(_ context.Context, traceID string) (*TraceDetailResp, error) {
	trace, err := s.repo.GetTraceByID(traceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get trace: %w", err)
	}
	return NewTraceDetailResp(trace), nil
}

func (s *Service) ListTraces(_ context.Context, req *ListTraceReq) (*dto.ListResp[TraceResp], error) {
	if req == nil {
		return nil, fmt.Errorf("list traces request is nil")
	}
	limit, offset := req.ToGormParams()
	filterOptions := req.ToFilterOptions()
	traces, total, err := s.repo.ListTraces(limit, offset, filterOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list traces: %w", err)
	}
	items := make([]TraceResp, 0, len(traces))
	for i := range traces {
		items = append(items, *NewTraceResp(&traces[i]))
	}
	return &dto.ListResp[TraceResp]{
		Items:      items,
		Pagination: req.ConvertToPaginationInfo(total),
	}, nil
}

func (s *Service) GetTraceStreamProcessor(ctx context.Context, traceID string) (*StreamProcessor, error) {
	algorithms, err := s.GetTraceStreamAlgorithms(ctx, traceID)
	if err != nil {
		return nil, err
	}
	return NewStreamProcessor(algorithms), nil
}

func (s *Service) GetTraceStreamAlgorithms(ctx context.Context, traceID string) ([]dto.ContainerVersionItem, error) {
	trace, err := s.repo.GetTraceByID(traceID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch trace: %w", err)
	}

	var algorithms []dto.ContainerVersionItem
	if trace.Type == consts.TraceTypeFullPipeline && s.redis.CheckCachedField(ctx, consts.InjectionAlgorithmsKey, trace.GroupID) {
		if err := s.redis.GetHashField(ctx, consts.InjectionAlgorithmsKey, trace.GroupID, &algorithms); err != nil {
			return nil, fmt.Errorf("failed to get algorithms from Redis: %w", err)
		}
	}

	if len(algorithms) == 0 {
		return nil, nil
	}

	filtered := algorithms[:0]
	for _, algorithm := range algorithms {
		if algorithm.ContainerName != config.GetDetectorName() {
			filtered = append(filtered, algorithm)
		}
	}
	return filtered, nil
}

// GetTraceSpans returns every OTel span the aegislab orchestrator emitted
// for traceID, fanned out from otel.otel_traces via the aegis trace_id
// SpanAttribute. The trace's existence in PostgreSQL is verified first so a
// 404 still distinguishes "no such aegis trace" from "trace exists but the
// OTel collector hasn't ingested anything yet". An empty list is a valid
// 200 response in the latter case.
func (s *Service) GetTraceSpans(ctx context.Context, traceID string) (*SpansResp, error) {
	if _, err := s.repo.GetTraceByID(traceID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: trace id: %s", consts.ErrNotFound, traceID)
		}
		return nil, fmt.Errorf("failed to load trace: %w", err)
	}
	if s.spans == nil {
		return nil, fmt.Errorf("clickhouse span reader not configured")
	}
	rows, err := s.spans.ReadSpansByTraceID(ctx, traceID)
	if err != nil {
		return nil, fmt.Errorf("read spans: %w", err)
	}
	out := &SpansResp{Spans: make([]SpanNode, 0, len(rows))}
	for _, r := range rows {
		endTS := r.Timestamp
		if r.DurationNanos > 0 {
			endTS = r.Timestamp.Add(time.Duration(r.DurationNanos))
		}
		out.Spans = append(out.Spans, SpanNode{
			OTelTraceID:  r.TraceID,
			SpanID:       r.SpanID,
			ParentSpanID: r.ParentSpanID,
			Service:      r.ServiceName,
			Op:           r.SpanName,
			StartTS:      r.Timestamp,
			EndTS:        endTS,
			Status:       r.StatusCode,
			Attrs:        r.SpanAttributes,
		})
	}
	return out, nil
}

func (s *Service) ReadTraceStreamMessages(ctx context.Context, streamKey, lastID string, count int64, block time.Duration) ([]goredis.XStream, error) {
	if lastID == "" {
		lastID = "0"
	}

	messages, err := s.redis.XRead(ctx, []string{streamKey, lastID}, count, block)
	if err != nil {
		return nil, fmt.Errorf("failed to read stream messages: %w", err)
	}
	return messages, nil
}
