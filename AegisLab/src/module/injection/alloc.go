package injection

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"aegis/config"
	"aegis/consts"
	redisinfra "aegis/infra/redis"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	goredis "github.com/redis/go-redis/v9"
)

// ErrPoolExhausted is returned by AllocateNamespaceForRestart when every
// 0..count-1 slot of a system is either lock-active or has no deployed
// workload, AND opts.AllowBootstrap is false. Callers should surface an
// actionable hint suggesting `aegisctl inject guided --install
// --namespace <system>N` to expand the pool — see #166 for the design
// tradeoffs.
var ErrPoolExhausted = errors.New("namespace pool exhausted: every slot is locked or has no deployed workload")

// AllocateOptions tunes the allocator behaviour without bloating the call
// signature. v1 has only one knob (AllowBootstrap); future flags drop in
// here.
type AllocateOptions struct {
	// AllowBootstrap, when true, lets the allocator extend the system's
	// count by 1 when no existing slot qualifies (PR-C, #166). The new
	// slot is reserved by locking the next ns name (<system><count>) and
	// bumping the chaos-system count via CountWriter. AllocateResult.Fresh
	// is set so callers know to skip submit-time BuildInjection for this
	// slot — RestartPedestal at runtime helm-installs before the
	// FaultInjection task runs.
	AllowBootstrap bool

	// CountWriter is required when AllowBootstrap is true. Used to bump
	// `injection.system.<system>.count` so config.GetAllNamespaces()
	// includes the new slot. Ignored when AllowBootstrap is false.
	CountWriter ChaosSystemWriter
}

// AllocateResult is the return shape of AllocateNamespaceForRestart.
type AllocateResult struct {
	// Namespace is the chosen pool slot (e.g. "sockshop3"). Always
	// non-empty on success.
	Namespace string
	// Fresh reports whether this allocation was satisfied by bumping the
	// pool (AllowBootstrap path) rather than by filling a hole. Fresh
	// slots have no workload at submit time and need RestartPedestal to
	// install before the inject task can find pods.
	Fresh bool
}

// WorkloadProbe checks whether `namespace` has at least one pod deployed.
// Injected by callers so tests don't need a live cluster. Production wiring
// uses a closure around k8s.Gateway.NamespaceHasWorkload.
type WorkloadProbe func(ctx context.Context, namespace string) (bool, error)

const (
	allocLockTTL        = 10 * time.Second
	allocLockKeyPattern = "alloc:%s"
)

// AllocateNamespaceForRestart claims a free, deployed slot for `system`,
// acquires its Redis namespace lock under `traceID`, and returns the chosen
// namespace name. Hole-fill only — walks 0..count-1 in ascending order and
// returns the first slot satisfying:
//
//   - currently not lock-active (no other trace owns the namespace lock or
//     the existing lock has expired by `now`)
//   - workload deployed (at least one pod present per `probe`)
//
// Returns ErrPoolExhausted when no qualifying slot exists. Returns other
// errors for Redis/probe failures.
//
// Race-safety: a per-system Redis SetNX lock at `alloc:<system>` (TTL 10s)
// serializes concurrent allocators so two parallel submits cannot both end
// up with the same slot. The chosen namespace is locked under `traceID`
// immediately so when RestartPedestal eventually runs and calls
// monitor.AcquireNamespaceForRestart with the same traceID, the
// same-owner re-acquire path treats it as success (see
// consumer/namespace_lock_store.go acquire(): TraceID == traceID
// short-circuits the busy check).
//
// Caller is responsible for setting `task.TraceID = traceID` before calling
// common.SubmitTaskWithDB so the eventual RestartPedestal's traceID matches
// the allocator's claim.
//
// NOTE: this lock-acquire path mirrors consumer/namespace_lock_store.go to
// keep the two callers (submit-time allocator vs. runtime monitor) writing
// to the same hash layout. If consumer's store changes its key format or
// fields, this code must follow. A shared package would be cleaner; not
// done here to avoid a chaossystem→consumer→injection cycle. See #166
// follow-ups.
func AllocateNamespaceForRestart(
	ctx context.Context,
	redis *redisinfra.Gateway,
	system string,
	endTime time.Time,
	traceID string,
	probe WorkloadProbe,
	opts AllocateOptions,
) (AllocateResult, error) {
	if redis == nil {
		return AllocateResult{}, fmt.Errorf("redis gateway required")
	}
	if traceID == "" {
		return AllocateResult{}, fmt.Errorf("traceID required")
	}
	if opts.AllowBootstrap && opts.CountWriter == nil {
		return AllocateResult{}, fmt.Errorf("AllowBootstrap requires CountWriter")
	}

	cfg, ok := config.GetChaosSystemConfigManager().Get(chaos.SystemType(system))
	if !ok {
		return AllocateResult{}, fmt.Errorf("system %q not registered", system)
	}
	template := nsTemplateFromPattern(cfg.NsPattern)
	if template == "" {
		return AllocateResult{}, fmt.Errorf("invalid ns_pattern for system %s: %q", system, cfg.NsPattern)
	}

	allocKey := fmt.Sprintf(allocLockKeyPattern, system)
	acquired, err := redis.SetNX(ctx, allocKey, traceID, allocLockTTL)
	if err != nil {
		return AllocateResult{}, fmt.Errorf("acquire allocator lock for %s: %w", system, err)
	}
	if !acquired {
		return AllocateResult{}, fmt.Errorf("allocator busy for system %s, retry shortly", system)
	}
	defer func() {
		_, _ = redis.DeleteKey(context.Background(), allocKey)
	}()

	now := time.Now()

	// Pass 1: hole-fill. Walk existing slots lowest-index first.
	for idx := 0; idx < cfg.Count; idx++ {
		ns := fmt.Sprintf(template, idx)

		active, lockErr := nsLockActive(ctx, redis, ns, now)
		if lockErr != nil {
			return AllocateResult{}, fmt.Errorf("check lock for %s: %w", ns, lockErr)
		}
		if active {
			continue
		}

		if probe != nil {
			hasWorkload, probeErr := probe(ctx, ns)
			if probeErr != nil {
				return AllocateResult{}, fmt.Errorf("probe workload in %s: %w", ns, probeErr)
			}
			if !hasWorkload {
				continue
			}
		}

		if err := nsLockAcquire(ctx, redis, ns, endTime, traceID, now); err != nil {
			// A concurrent runtime task may have grabbed it after our
			// active-check; try the next slot rather than fail the whole
			// allocation.
			continue
		}
		return AllocateResult{Namespace: ns, Fresh: false}, nil
	}

	// Pass 2: bootstrap a fresh slot at index = count, if allowed.
	if !opts.AllowBootstrap {
		return AllocateResult{}, ErrPoolExhausted
	}
	freshNs := fmt.Sprintf(template, cfg.Count)
	bumped, bumpErr := opts.CountWriter.EnsureCountForNamespace(ctx, system, freshNs)
	if bumpErr != nil {
		return AllocateResult{}, fmt.Errorf("bootstrap-allocate: bump count for %s to register %s: %w", system, freshNs, bumpErr)
	}
	if !bumped {
		// Count already covered this index — race with another allocator
		// that just bumped. Fall through and lock anyway; if THAT
		// allocator also locked it, our acquire returns busy and we
		// fail clearly.
	}
	if err := nsLockAcquire(ctx, redis, freshNs, endTime, traceID, now); err != nil {
		return AllocateResult{}, fmt.Errorf("bootstrap-allocate: lock new slot %s: %w", freshNs, err)
	}
	return AllocateResult{Namespace: freshNs, Fresh: true}, nil
}

// nsTemplateFromPattern mirrors config.convertPatternToTemplate (private
// there). Converts "^sockshop\d+$" → "sockshop%d" so the allocator can
// fmt.Sprintf each candidate slot.
func nsTemplateFromPattern(pattern string) string {
	template := pattern
	if len(template) > 0 && template[0] == '^' {
		template = template[1:]
	}
	if len(template) > 0 && template[len(template)-1] == '$' {
		template = template[:len(template)-1]
	}
	return regexp.MustCompile(`\\d\+`).ReplaceAllString(template, "%d")
}

// nsLockActive mirrors consumer/namespace_lock_store.go isActive(). Returns
// true when the namespace lock has a non-empty trace_id and end_time is in
// the future. Uses the same monitor:ns:<ns> hash layout.
func nsLockActive(ctx context.Context, redis *redisinfra.Gateway, ns string, now time.Time) (bool, error) {
	key := fmt.Sprintf(consts.NamespaceKeyPattern, ns)
	endTimeStr, err := redis.HashGet(ctx, key, "end_time")
	if err != nil && err != goredis.Nil {
		return false, err
	}
	traceID, err := redis.HashGet(ctx, key, "trace_id")
	if err != nil && err != goredis.Nil {
		return false, err
	}
	if traceID == "" || endTimeStr == "" {
		return false, nil
	}
	endTime, err := strconv.ParseInt(endTimeStr, 10, 64)
	if err != nil {
		return false, err
	}
	return now.Unix() < endTime, nil
}

// nsLockAcquire mirrors consumer/namespace_lock_store.go acquire(). Atomic
// WATCH+MULTI upsert of (end_time, trace_id) on monitor:ns:<ns>. Same Redis
// layout as the consumer-side lock store so a subsequent
// monitor.AcquireNamespaceForRestart with the same traceID re-acquires
// idempotently.
func nsLockAcquire(ctx context.Context, redis *redisinfra.Gateway, ns string, endTime time.Time, traceID string, now time.Time) error {
	key := fmt.Sprintf(consts.NamespaceKeyPattern, ns)
	return redis.Watch(ctx, func(tx *goredis.Tx) error {
		endTimeStr, err := tx.HGet(ctx, key, "end_time").Result()
		if err != nil && err != goredis.Nil {
			return err
		}
		existingTrace, err := tx.HGet(ctx, key, "trace_id").Result()
		if err != nil && err != goredis.Nil {
			return err
		}
		if existingTrace != "" && existingTrace != traceID && endTimeStr != "" {
			existingEnd, parseErr := strconv.ParseInt(endTimeStr, 10, 64)
			if parseErr == nil && now.Unix() < existingEnd {
				return fmt.Errorf("namespace %s locked by %s", ns, existingTrace)
			}
		}
		_, err = tx.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
			pipe.HSet(ctx, key, "end_time", endTime.Unix())
			pipe.HSet(ctx, key, "trace_id", traceID)
			return nil
		})
		return err
	}, key)
}
