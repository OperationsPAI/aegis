package injection

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
	redisinfra "aegis/platform/redis"

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
	// allocLockTTL is the safety-net TTL for the per-system allocator
	// lock. It is NOT the expected hold time — defer-CompareAndDelete is
	// the normal release path. This must comfortably cover the worst-case
	// allocation latency (etcd write in EnsureCountForNamespace, pod-list
	// probes across every slot, viper reload). Bumped from 10s to 60s
	// after PR #167 review (#166 hardening): real-world allocations have
	// crossed 10s under k8s API hiccups, allowing the original lock to
	// expire mid-allocation and a successor allocator's lock to be
	// blown away by the deferred DEL.
	allocLockTTL        = 60 * time.Second
	allocLockKeyPattern = "alloc:%s"
)

// ErrNamespaceLocked is the sentinel returned by nsLockAcquire when the
// candidate namespace is currently held by a different traceID. The
// allocator catches this exact error class to skip the slot and continue
// scanning; any other error (Redis network, parse, watch retry) aborts
// allocation so the caller sees the real cause instead of a misleading
// ErrPoolExhausted.
var ErrNamespaceLocked = errors.New("namespace already locked by another trace")

// nsLockProbeFn / nsLockAcquireFn are package-level seams for tests: real
// production code wires them to nsLockActive / nsLockAcquire (the Redis
// implementations). Tests substitute fakes to drive specific error classes
// (e.g. simulated network failure) through the allocator without a live
// Redis. Restored to the real impls via t.Cleanup.
var (
	nsLockProbeFn       = nsLockActive
	nsLockAcquireFn     = nsLockAcquire
	allocSetNXFn        = func(ctx context.Context, r *redisinfra.Gateway, key, val string, ttl time.Duration) (bool, error) {
		return r.SetNX(ctx, key, val, ttl)
	}
	allocCompareDelFn = func(ctx context.Context, r *redisinfra.Gateway, key, val string) (int64, error) {
		return r.CompareAndDeleteKey(ctx, key, val)
	}
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
// Race-safety: a per-system Redis SetNX lock at `alloc:<system>` (TTL is
// allocLockTTL — a safety net, NOT a deadline; defer-CompareAndDelete is
// the normal release path) serializes concurrent allocators so two
// parallel submits cannot both end up with the same slot. The chosen namespace is locked under `traceID`
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
	acquired, err := allocSetNXFn(ctx, redis, allocKey, traceID, allocLockTTL)
	if err != nil {
		return AllocateResult{}, fmt.Errorf("acquire allocator lock for %s: %w", system, err)
	}
	if !acquired {
		return AllocateResult{}, fmt.Errorf("allocator busy for system %s, retry shortly", system)
	}
	defer func() {
		// CompareAndDelete (not DeleteKey): release the alloc lock only
		// if its stored value still matches our traceID. Guards against
		// the case where allocLockTTL expired mid-allocation and a
		// successor allocator now owns the same allocKey — a naive DEL
		// would silently destroy the successor's lock and let two
		// allocators race on the same system. See #167 Copilot review.
		_, _ = allocCompareDelFn(context.Background(), redis, allocKey, traceID)
	}()

	now := time.Now()

	// Pass 1: hole-fill. Walk existing slots lowest-index first. Prefer
	// slots that already have a workload (cheap reuse — RestartPedestal
	// can helm-upgrade in place). Track the lowest-index unlocked-but-
	// empty slot so Pass 1.5 can recycle it when the operator deleted the
	// underlying namespace between rounds. Without this fallback, deleted
	// slots looked identical to never-existed ones and the allocator
	// always grew the pool via Pass 2; see #227.
	emptySlotIdx := -1
	for idx := 0; idx < cfg.Count; idx++ {
		ns := fmt.Sprintf(template, idx)

		active, lockErr := nsLockProbeFn(ctx, redis, ns, now)
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
				// Remember the lowest-index empty hole; Pass 1.5
				// will fill it (treated as Fresh) after the
				// workload-bearing scan completes.
				if opts.AllowBootstrap && emptySlotIdx == -1 {
					emptySlotIdx = idx
				}
				continue
			}
		}

		if err := nsLockAcquireFn(ctx, redis, ns, endTime, traceID, now); err != nil {
			// Only skip-and-continue on the explicit "another trace
			// owns this slot" race. Real Redis/network/parse failures
			// must abort allocation rather than be silently swallowed
			// into ErrPoolExhausted, which would mask the underlying
			// fault from the submit handler. See #167 Copilot review.
			if errors.Is(err, ErrNamespaceLocked) {
				continue
			}
			return AllocateResult{}, fmt.Errorf("acquire ns lock for %s: %w", ns, err)
		}
		return AllocateResult{Namespace: ns, Fresh: false}, nil
	}

	// Pass 1.5: hole-fill into a deleted-but-counted slot before extending
	// the pool. This is the fix for #227: an operator who deletes
	// namespaces between rounds (autonomous inject-loop campaign) used to
	// see count grow monotonically because every freed slot fell through
	// to Pass 2 instead of being reused. Treated as Fresh because the
	// caller must helm-install before BuildInjection can list pods —
	// identical post-conditions to Pass 2.
	if emptySlotIdx >= 0 {
		ns := fmt.Sprintf(template, emptySlotIdx)
		if err := nsLockAcquireFn(ctx, redis, ns, endTime, traceID, now); err != nil {
			if !errors.Is(err, ErrNamespaceLocked) {
				return AllocateResult{}, fmt.Errorf("recycle empty slot %s: %w", ns, err)
			}
			// Race: a peer allocator grabbed the empty slot between
			// our scan and our lock. Fall through to bootstrap.
		} else {
			return AllocateResult{Namespace: ns, Fresh: true}, nil
		}
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
	if err := nsLockAcquireFn(ctx, redis, freshNs, endTime, traceID, now); err != nil {
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
				return fmt.Errorf("%w: namespace %s held by trace %s", ErrNamespaceLocked, ns, existingTrace)
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
