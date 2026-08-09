// Package ratelimiter provides admin/operator APIs over the token-bucket
// rate limiters that back the restart_pedestal, build_container and
// algo_execution concurrency gates. Operators can inspect bucket state,
// reset a bucket, or garbage-collect tokens still held by terminal-state
// tasks (OperationsPAI/aegis#21).
package ratelimiter

import (
	"context"
	"fmt"
	"strings"

	runtimeclient "aegis/clients/runtime"
	"aegis/platform/consts"
	redisinfra "aegis/platform/redis"
	"aegis/platform/model"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

const tokenBucketKeyPrefix = "token_bucket:"

// Service exposes rate-limiter admin operations. It is deliberately thin —
// all the state lives in Redis/MySQL and we just wrap scans + set ops.
type Service struct {
	redis   *redisinfra.Gateway
	db      *gorm.DB
	runtime *runtimeclient.Client
}

// ServiceParams carries the rate-limiter admin dependencies. The runtime
// client is optional: it is wired in modes that run the runtime-worker query
// channel (both / consumer / runtime-worker) but absent in the lean aegis-api
// graph, where status falls back to the const ceiling.
type ServiceParams struct {
	fx.In

	Redis   *redisinfra.Gateway
	DB      *gorm.DB
	Runtime *runtimeclient.Client `optional:"true"`
}

func NewService(p ServiceParams) *Service {
	return &Service{redis: p.Redis, db: p.DB, runtime: p.Runtime}
}

// knownBuckets returns the canonical SET of token-bucket keys. Capacity is
// no longer sourced here — the const ceiling is a compile-time guess, not
// what the live limiter is actually enforcing after an operator override.
// Callers overlay live capacity from the runtime-worker; see liveCapacities.
func knownBuckets() map[string]struct{} {
	return map[string]struct{}{
		consts.RestartPedestalTokenBucket:  {},
		consts.NamespaceWarmingTokenBucket: {},
		consts.BuildContainerTokenBucket:   {},
		consts.AlgoExecutionTokenBucket:    {},
		consts.BuildDatapackTokenBucket:    {},
	}
}

// constCapacity is the compile-time default ceiling for a bucket, used only
// as a fallback when the runtime-worker can't be reached. The live limiter's
// Snapshot().MaxTokens is authoritative; this keeps status rendering a number
// rather than 0 when the gRPC query channel is down or unconfigured.
func constCapacity(bucketKey string) int {
	switch bucketKey {
	case consts.RestartPedestalTokenBucket:
		return consts.MaxConcurrentRestartPedestal
	case consts.NamespaceWarmingTokenBucket:
		return consts.MaxConcurrentNamespaceWarming
	case consts.BuildContainerTokenBucket:
		return consts.MaxConcurrentBuildContainer
	case consts.AlgoExecutionTokenBucket:
		return consts.MaxConcurrentAlgoExecution
	case consts.BuildDatapackTokenBucket:
		return consts.MaxConcurrentBuildDatapack
	default:
		return 0
	}
}

// liveCapacities pulls each limiter's live MaxTokens from the runtime-worker
// over gRPC. Returns nil when the query channel is unconfigured or errors
// (callers then fall back to constCapacity).
func liveCapacities(ctx context.Context, rt *runtimeclient.Client) map[string]int {
	if rt == nil || !rt.Enabled() {
		return nil
	}
	caps, err := rt.GetLimiterStatus(ctx)
	if err != nil {
		logrus.WithError(err).Warn("rate-limiter status: live capacity query failed; falling back to const ceiling")
		return nil
	}
	out := make(map[string]int, len(caps))
	for _, c := range caps {
		out[c.BucketKey] = c.MaxTokens
	}
	return out
}

// resolveCapacity picks the live MaxTokens for a bucket when the runtime-worker
// reported it, otherwise the compile-time const ceiling.
func resolveCapacity(bucketKey string, live map[string]int) int {
	if cap, ok := live[bucketKey]; ok {
		return cap
	}
	return constCapacity(bucketKey)
}

// isTerminalState mirrors the codebase's TaskCompleted (3), TaskError (-1),
// TaskCancelled (-2) states.
func isTerminalState(state consts.TaskState) bool {
	return state == consts.TaskCompleted || state == consts.TaskError || state == consts.TaskCancelled
}

// List returns each token_bucket:* bucket with its holders. Capacity is the
// live MaxTokens reported by the runtime-worker that owns the limiter; the
// const ceiling is a fallback only when that query is unavailable.
func (s *Service) List(ctx context.Context) (*RateLimiterListResp, error) {
	buckets := knownBuckets()

	extra, err := s.redis.ScanKeys(ctx, tokenBucketKeyPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("scan token buckets: %w", err)
	}
	for _, key := range extra {
		buckets[key] = struct{}{}
	}

	live := liveCapacities(ctx, s.runtime)

	items := make([]RateLimiterItem, 0, len(buckets))
	for key := range buckets {
		capacity := resolveCapacity(key, live)
		holders, err := s.redis.SetMembers(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("smembers %s: %w", key, err)
		}
		holderItems := make([]RateLimiterHolder, 0, len(holders))
		for _, taskID := range holders {
			state, found, err := s.lookupTaskState(ctx, taskID)
			if err != nil {
				logrus.WithError(err).WithField("task_id", taskID).
					Warn("lookup task state for rate-limiter holder")
			}
			stateName := "Unknown"
			terminal := false
			if found {
				stateName = consts.GetTaskStateName(state)
				terminal = isTerminalState(state)
			} else {
				terminal = true
			}
			holderItems = append(holderItems, RateLimiterHolder{
				TaskID: taskID, TaskState: stateName, IsTerminal: terminal,
			})
		}
		items = append(items, RateLimiterItem{
			Bucket:   strings.TrimPrefix(key, tokenBucketKeyPrefix),
			Key:      key,
			Capacity: capacity,
			Held:     len(holders),
			Holders:  holderItems,
		})
	}
	return &RateLimiterListResp{Items: items}, nil
}

// Reset deletes the given bucket key from Redis.
func (s *Service) Reset(ctx context.Context, bucket string) error {
	key := resolveBucketKey(bucket)
	if _, ok := knownBuckets()[key]; !ok {
		return fmt.Errorf("%w: unknown bucket %q", consts.ErrBadRequest, bucket)
	}
	n, err := s.redis.DeleteKey(ctx, key)
	if err != nil {
		return fmt.Errorf("del %s: %w", key, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: bucket %q not present in redis", consts.ErrNotFound, bucket)
	}
	logrus.WithField("bucket", key).Warn("rate-limiter bucket reset")
	return nil
}

// GC releases tokens held by terminal-state (or unknown) tasks across all
// known buckets. Returns (released, touchedBuckets, err).
func (s *Service) GC(ctx context.Context) (int, int, error) {
	return gcWith(ctx, s.redis, s.db, knownBuckets())
}

// gcWith is the testable core.
func gcWith(ctx context.Context, r *redisinfra.Gateway, db *gorm.DB, buckets map[string]struct{}) (int, int, error) {
	var released, touched int
	for key := range buckets {
		holders, err := r.SetMembers(ctx, key)
		if err != nil {
			return released, touched, fmt.Errorf("smembers %s: %w", key, err)
		}
		if len(holders) == 0 {
			continue
		}
		var toRelease []any
		for _, taskID := range holders {
			state, found, lerr := lookupTaskStateWith(ctx, db, taskID)
			if lerr != nil {
				logrus.WithError(lerr).WithField("task_id", taskID).
					Warn("gc: lookup task state failed; skipping")
				continue
			}
			if !found || isTerminalState(state) {
				toRelease = append(toRelease, taskID)
			}
		}
		if len(toRelease) == 0 {
			continue
		}
		n, rerr := r.SetRemove(ctx, key, toRelease...)
		if rerr != nil {
			return released, touched, fmt.Errorf("srem %s: %w", key, rerr)
		}
		if n > 0 {
			released += int(n)
			touched++
			logrus.WithFields(logrus.Fields{
				"bucket": key, "released": n, "holders": toRelease,
			}).Warn("rate-limiter gc: released leaked tokens")
		}
	}
	return released, touched, nil
}

func (s *Service) lookupTaskState(ctx context.Context, taskID string) (consts.TaskState, bool, error) {
	return lookupTaskStateWith(ctx, s.db, taskID)
}

func lookupTaskStateWith(ctx context.Context, db *gorm.DB, taskID string) (consts.TaskState, bool, error) {
	var task model.Task
	err := db.WithContext(ctx).Select("state").Where("id = ?", taskID).First(&task).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return 0, false, nil
		}
		return 0, false, err
	}
	return task.State, true, nil
}

func resolveBucketKey(name string) string {
	if strings.HasPrefix(name, tokenBucketKeyPrefix) {
		return name
	}
	return tokenBucketKeyPrefix + name
}
