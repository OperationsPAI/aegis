package state

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"aegis/platform/consts"
	redisinfra "aegis/platform/redis"

	goredis "github.com/redis/go-redis/v9"
)

type LockState struct {
	EndTime int64
	TraceID string
}

type LockStore struct {
	client *redisinfra.Gateway
}

func NewLockStore(client *redisinfra.Gateway) LockStore {
	return LockStore{client: client}
}

func (s LockStore) Key(namespace string) string {
	return fmt.Sprintf(consts.NamespaceKeyPattern, namespace)
}

func (s LockStore) Read(ctx context.Context, namespace string) (*LockState, error) {
	endTimeStr, err := s.client.HashGet(ctx, s.Key(namespace), "end_time")
	if err != nil && err != goredis.Nil {
		return nil, err
	}

	traceID, err := s.client.HashGet(ctx, s.Key(namespace), "trace_id")
	if err != nil && err != goredis.Nil {
		return nil, err
	}

	if endTimeStr == "" {
		return &LockState{TraceID: traceID}, nil
	}

	endTime, err := strconv.ParseInt(endTimeStr, 10, 64)
	if err != nil {
		return nil, err
	}

	return &LockState{EndTime: endTime, TraceID: traceID}, nil
}

func (s LockStore) ReadFromHash(reader goredis.HashCmdable, ctx context.Context, namespace string) (*LockState, error) {
	endTimeStr, err := reader.HGet(ctx, s.Key(namespace), "end_time").Result()
	if err != nil && err != goredis.Nil {
		return nil, err
	}

	traceID, err := reader.HGet(ctx, s.Key(namespace), "trace_id").Result()
	if err != nil && err != goredis.Nil {
		return nil, err
	}

	if endTimeStr == "" {
		return &LockState{TraceID: traceID}, nil
	}

	endTime, err := strconv.ParseInt(endTimeStr, 10, 64)
	if err != nil {
		return nil, err
	}

	return &LockState{EndTime: endTime, TraceID: traceID}, nil
}

func (s LockStore) Write(ctx context.Context, namespace string, endTime int64, traceID string) error {
	return s.client.HashSet(ctx, s.Key(namespace), map[string]any{
		"end_time": endTime,
		"trace_id": traceID,
	})
}

func (s LockStore) Acquire(ctx context.Context, namespace string, endTime time.Time, traceID string, now time.Time) error {
	return s.client.Watch(ctx, func(tx *goredis.Tx) error {
		state, err := s.ReadFromHash(tx, ctx, namespace)
		if err != nil {
			return err
		}
		if state.TraceID != "" && state.TraceID != traceID && now.Unix() < state.EndTime {
			return fmt.Errorf("namespace %s is locked by %s until %v",
				namespace, state.TraceID, time.Unix(state.EndTime, 0).Format(time.RFC3339))
		}
		_, err = tx.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
			pipe.HSet(ctx, s.Key(namespace), "end_time", endTime.Unix())
			pipe.HSet(ctx, s.Key(namespace), "trace_id", traceID)
			return nil
		})
		return err
	}, s.Key(namespace))
}

func (s LockStore) Release(ctx context.Context, namespace, traceID string, releasedAt time.Time) error {
	state, err := s.Read(ctx, namespace)
	if err != nil && err != goredis.Nil {
		return fmt.Errorf("failed to get current trace_id: %v", err)
	}
	if state != nil && state.TraceID != traceID && state.TraceID != "" {
		return fmt.Errorf("cannot release lock: namespace %s is not owned by trace_id %s (current owner: %s)",
			namespace, traceID, state.TraceID)
	}
	return s.Write(ctx, namespace, releasedAt.Unix(), "")
}

func (s LockStore) IsActive(ctx context.Context, namespace string, now time.Time) (bool, error) {
	state, err := s.Read(ctx, namespace)
	if err == goredis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state.TraceID == "" {
		return false, nil
	}
	return now.Unix() < state.EndTime, nil
}
