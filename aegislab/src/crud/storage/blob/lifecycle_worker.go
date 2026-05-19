package blob

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	redisinfra "aegis/platform/redis"

	"aegis/platform/config"
	"aegis/platform/dto"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

// BucketLifecycleWorker enforces persisted BucketLifecycle.Rules. It is
// the missing half of `lifecycleExecutionDeferred`: the rule shape
// already round-trips through the DB column, but until this worker
// landed nothing consumed it.
//
// Two-stage GC, separated by `grace`:
//
//   - Stage 1 (soft-delete): for every bucket with a configured policy,
//     find objects whose storage_key starts with the rule's MatchPrefix
//     and whose created_at is older than ExpireAfterDays. Mark them as
//     soft-deleted by setting deleted_at. ObjectRecord rows live on so
//     an operator has 24h to spot a mis-configured rule.
//   - Stage 2 (hard-delete): for every row whose deleted_at is older
//     than `grace`, drive the driver-side Delete and HardDelete the
//     row. This piggybacks on the existing DeletionWorker's deletePass
//     so we don't duplicate driver wiring — the worker only runs the
//     soft-delete stage and relies on the deferred hard delete to
//     finish the job.
//
// Distributed lock: multiple replicas share one cluster-wide Redis lock
// (`blob:lifecycle:sweep:lock`) so only one replica sweeps per period.
// The lock TTL acts as a safety net — if a replica crashes mid-sweep
// the lock expires and the next tick proceeds. The lock is released
// via compare-and-delete to avoid the classic SetNX-with-TTL race (see
// CompareAndDeleteKey in platform/redis/gateway.go).
type BucketLifecycleWorker struct {
	repo     *Repository
	registry *Registry
	redis    *redisinfra.Gateway
	clock    Clock
	interval time.Duration
	grace    time.Duration
	batch    int
	enabled  bool
	lockTTL  time.Duration

	cancelTick context.CancelFunc
}

// BucketLifecycleWorkerConfig captures the knobs the bucket-lifecycle
// worker reads from `[blob.lifecycle]`. Zero values fall back to safe
// defaults (1h sweep, 24h grace).
type BucketLifecycleWorkerConfig struct {
	Interval time.Duration
	Grace    time.Duration
	Batch    int
	Enabled  bool
}

func loadBucketLifecycleWorkerConfig() BucketLifecycleWorkerConfig {
	cfg := BucketLifecycleWorkerConfig{
		Interval: parseDurationOrZero(config.GetString("blob.lifecycle.sweep_interval")),
		Grace:    parseDurationOrZero(config.GetString("blob.lifecycle.grace_period")),
		Batch:    config.GetInt("blob.lifecycle.bucket_batch"),
		// Default OFF. Operator flips after observing dry-runs match
		// expectation on a real cluster — see the task brief.
		Enabled: config.GetBool("blob.lifecycle.enabled"),
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	if cfg.Grace <= 0 {
		cfg.Grace = 24 * time.Hour
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 500
	}
	return cfg
}

// NewBucketLifecycleWorker constructs the worker. `gw` may be nil — in
// that case the worker still runs but skips the distributed lock (so
// single-process tests and dev binaries work without Redis). Multi-
// replica deployments must wire a real Gateway or two replicas may
// sweep concurrently.
func NewBucketLifecycleWorker(repo *Repository, reg *Registry, gw *redisinfra.Gateway, clock Clock) *BucketLifecycleWorker {
	cfg := loadBucketLifecycleWorkerConfig()
	return &BucketLifecycleWorker{
		repo:     repo,
		registry: reg,
		redis:    gw,
		clock:    clock,
		interval: cfg.Interval,
		grace:    cfg.Grace,
		batch:    cfg.Batch,
		enabled:  cfg.Enabled,
		// Lock TTL is intentionally longer than the sweep is expected
		// to take. Tune via the sweep_interval if you observe contention.
		lockTTL: 10 * time.Minute,
	}
}

// LifecycleSweepResult is returned by Run and DryRun: a per-bucket
// summary of what was (or would be) soft-deleted this pass. Empty
// `Buckets` means no policies are configured anywhere.
type LifecycleSweepResult struct {
	Buckets    []LifecycleSweepBucket `json:"buckets"`
	LockHeld   bool                   `json:"lock_held"`
	SweptAt    time.Time              `json:"swept_at"`
	GracePeriod string                `json:"grace_period"`
}

// LifecycleSweepBucket is the per-bucket breakdown.
type LifecycleSweepBucket struct {
	Bucket    string                       `json:"bucket"`
	Rules     []LifecycleSweepRule         `json:"rules"`
	TotalKeys int                          `json:"total_keys"`
	Examples  []string                     `json:"examples,omitempty"`
}

// LifecycleSweepRule is the per-rule preview / actuals.
type LifecycleSweepRule struct {
	Name            string `json:"name"`
	MatchPrefix     string `json:"match_prefix"`
	ExpireAfterDays int    `json:"expire_after_days"`
	Matched         int    `json:"matched"`
}

const (
	bucketLifecycleSweepLockKey = "blob:lifecycle:sweep:lock"
	// At most this many example storage-keys are surfaced per bucket
	// in the dry-run result. Keeps the JSON payload bounded.
	bucketLifecycleSweepMaxExamples = 5
)

// Package-level seams for tests: production code wires these to the
// Redis gateway implementations. Tests substitute fakes to exercise
// the lock contention path without a live Redis. Restored to the real
// impls via t.Cleanup.
var (
	BucketLifecycleSetNXFn = func(ctx context.Context, gw *redisinfra.Gateway, key, value string, ttl time.Duration) (bool, error) {
		return gw.SetNX(ctx, key, value, ttl)
	}
	BucketLifecycleCompareDelFn = func(ctx context.Context, gw *redisinfra.Gateway, key, value string) (int64, error) {
		return gw.CompareAndDeleteKey(ctx, key, value)
	}
)

// DryRun previews what the next sweep would touch without writing. If
// onlyBucket is non-empty the preview is limited to that bucket; an
// unknown bucket is reported as ErrBucketNotFound.
func (w *BucketLifecycleWorker) DryRun(ctx context.Context, onlyBucket string) (*LifecycleSweepResult, error) {
	now := w.clock.Now()
	res := &LifecycleSweepResult{SweptAt: now, GracePeriod: w.grace.String()}

	buckets, err := w.candidateBuckets(onlyBucket)
	if err != nil {
		return nil, err
	}
	for _, b := range buckets {
		bs, err := w.previewBucket(ctx, b, now)
		if err != nil {
			return nil, fmt.Errorf("preview bucket %q: %w", b.Config.Name, err)
		}
		if bs == nil {
			continue
		}
		res.Buckets = append(res.Buckets, *bs)
	}
	return res, nil
}

// Run executes one sweep pass. Acquires the distributed lock, walks
// every bucket with a Lifecycle policy, soft-deletes matching rows.
// The DeletionWorker's deletePass picks them up after `grace` (the
// dedicated DeletionWorker already filters by deleted_at; we only
// soft-delete rows where deleted_at + grace ≤ now, by deferring the
// soft-delete itself until the row has matched the rule for ≥ grace
// would be wrong — instead the row is marked at first match and the
// downstream hard-delete waits for grace to pass).
//
// Errors on individual rules are logged and the sweep continues — one
// broken rule should not stall the rest.
func (w *BucketLifecycleWorker) Run(ctx context.Context) (*LifecycleSweepResult, error) {
	acquired, release, err := w.acquireLock(ctx)
	if err != nil {
		return nil, err
	}
	if !acquired {
		logrus.Debug("blob: bucket-lifecycle sweep skipped — lock held by another replica")
		return &LifecycleSweepResult{LockHeld: false, SweptAt: w.clock.Now()}, nil
	}
	defer release()

	now := w.clock.Now()
	res := &LifecycleSweepResult{LockHeld: true, SweptAt: now, GracePeriod: w.grace.String()}

	buckets, err := w.candidateBuckets("")
	if err != nil {
		return nil, err
	}
	for _, b := range buckets {
		bs, err := w.sweepBucket(ctx, b, now)
		if err != nil {
			logrus.WithError(err).WithField("bucket", b.Config.Name).
				Warn("blob: bucket-lifecycle sweep bucket failed")
			continue
		}
		if bs == nil {
			continue
		}
		res.Buckets = append(res.Buckets, *bs)
		logrus.WithFields(logrus.Fields{
			"bucket": bs.Bucket,
			"keys":   bs.TotalKeys,
			"rules":  len(bs.Rules),
		}).Info("blob: bucket-lifecycle sweep batch")
	}
	return res, nil
}

// candidateBuckets returns the in-memory Bucket objects that have a
// non-empty lifecycle policy. If onlyBucket is non-empty the result is
// filtered to that single bucket (or ErrBucketNotFound).
func (w *BucketLifecycleWorker) candidateBuckets(onlyBucket string) ([]*Bucket, error) {
	if onlyBucket != "" {
		b, err := w.registry.Lookup(onlyBucket)
		if err != nil {
			return nil, err
		}
		if b.Config.Lifecycle == nil || len(b.Config.Lifecycle.Rules) == 0 {
			return nil, nil
		}
		return []*Bucket{b}, nil
	}
	names := w.registry.Names()
	out := make([]*Bucket, 0, len(names))
	for _, n := range names {
		b, err := w.registry.Lookup(n)
		if err != nil {
			continue
		}
		if b.Config.Lifecycle == nil || len(b.Config.Lifecycle.Rules) == 0 {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

func (w *BucketLifecycleWorker) previewBucket(ctx context.Context, b *Bucket, now time.Time) (*LifecycleSweepBucket, error) {
	out := &LifecycleSweepBucket{Bucket: b.Config.Name}
	for _, rule := range b.Config.Lifecycle.Rules {
		rows, err := listMatchingObjects(ctx, w.repo.DB, b.Config.Name, rule, now, w.batch)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", rule.Name, err)
		}
		out.Rules = append(out.Rules, LifecycleSweepRule{
			Name:            rule.Name,
			MatchPrefix:     rule.MatchPrefix,
			ExpireAfterDays: rule.ExpireAfterDays,
			Matched:         len(rows),
		})
		out.TotalKeys += len(rows)
		for _, r := range rows {
			if len(out.Examples) >= bucketLifecycleSweepMaxExamples {
				break
			}
			out.Examples = append(out.Examples, r.StorageKey)
		}
	}
	if out.TotalKeys == 0 && len(out.Rules) == 0 {
		return nil, nil
	}
	return out, nil
}

func (w *BucketLifecycleWorker) sweepBucket(ctx context.Context, b *Bucket, now time.Time) (*LifecycleSweepBucket, error) {
	out := &LifecycleSweepBucket{Bucket: b.Config.Name}
	for _, rule := range b.Config.Lifecycle.Rules {
		rows, err := listMatchingObjects(ctx, w.repo.DB, b.Config.Name, rule, now, w.batch)
		if err != nil {
			return nil, fmt.Errorf("rule %q list: %w", rule.Name, err)
		}
		var deleted int
		for _, r := range rows {
			if err := w.repo.MarkExpired(ctx, r.ID, now); err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"bucket": b.Config.Name,
					"key":    r.StorageKey,
					"rule":   rule.Name,
				}).Warn("blob: bucket-lifecycle soft-delete failed")
				continue
			}
			deleted++
		}
		out.Rules = append(out.Rules, LifecycleSweepRule{
			Name:            rule.Name,
			MatchPrefix:     rule.MatchPrefix,
			ExpireAfterDays: rule.ExpireAfterDays,
			Matched:         deleted,
		})
		out.TotalKeys += deleted
	}
	if out.TotalKeys == 0 {
		return nil, nil
	}
	return out, nil
}

// listMatchingObjects is the predicate the worker and dry-run share:
// pick rows in `bucket` whose storage_key has the rule's MatchPrefix
// and whose created_at is older than ExpireAfterDays from `now`. Soft-
// deleted rows are excluded (the downstream deletePass owns them).
//
// MatchPrefix is treated as a literal prefix; LIKE special chars in
// user input are escaped so a prefix of `foo%bar` does not turn into
// a wildcard. Empty prefix means "every key in the bucket".
func listMatchingObjects(ctx context.Context, db *gorm.DB, bucket string, rule BucketLifecycleRule, now time.Time, limit int) ([]ObjectRecord, error) {
	if limit <= 0 {
		limit = 500
	}
	cutoff := now.Add(-time.Duration(rule.ExpireAfterDays) * 24 * time.Hour)
	q := db.WithContext(ctx).
		Where("bucket = ? AND deleted_at IS NULL AND created_at < ?", bucket, cutoff)
	if rule.MatchPrefix != "" {
		escaped := escapeSQLLike(rule.MatchPrefix)
		q = q.Where("storage_key LIKE ? ESCAPE '\\'", escaped+"%")
	}
	var rows []ObjectRecord
	if err := q.Order("id asc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// escapeSQLLike escapes the three LIKE meta-characters with a single
// backslash so a configured MatchPrefix is matched as a literal string.
// Backslash itself is escaped first to avoid double-substitution.
func escapeSQLLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// acquireLock acquires the cluster-wide sweep lock. Returns
// (acquired=true, release=fn, nil) on success; (false, no-op, nil) when
// another replica holds it; (_, _, err) on Redis failure. If Redis is
// not wired (nil gateway) the lock is treated as acquired — single-
// process callers (tests, dev) rely on this path.
func (w *BucketLifecycleWorker) acquireLock(ctx context.Context) (bool, func(), error) {
	if w.redis == nil {
		return true, func() {}, nil
	}
	// Token value is unique per acquisition so the release path can
	// compare-and-delete without risk of deleting a successor's lock.
	token := fmt.Sprintf("%d", w.clock.Now().UnixNano())
	ok, err := BucketLifecycleSetNXFn(ctx, w.redis, bucketLifecycleSweepLockKey, token, w.lockTTL)
	if err != nil {
		return false, func() {}, fmt.Errorf("acquire sweep lock: %w", err)
	}
	if !ok {
		return false, func() {}, nil
	}
	release := func() {
		// background ctx so a cancelled sweep still releases its lock.
		_, _ = BucketLifecycleCompareDelFn(context.Background(), w.redis, bucketLifecycleSweepLockKey, token)
	}
	return true, release, nil
}

// registerBucketLifecycle is the fx hook that starts the periodic sweep
// on app start. Disabled by default (`blob.lifecycle.enabled=false`)
// so the first deploy on a cluster gives the operator time to run a
// dry-run before bytes start disappearing.
func registerBucketLifecycle(lc fx.Lifecycle, w *BucketLifecycleWorker) {
	if !w.enabled {
		logrus.Info("blob: bucket-lifecycle worker disabled (set blob.lifecycle.enabled=true to turn on)")
		return
	}
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			ctx, cancel := context.WithCancel(context.Background())
			w.cancelTick = cancel
			go w.loop(ctx)
			logrus.WithFields(logrus.Fields{
				"interval": w.interval,
				"grace":    w.grace,
			}).Info("blob: bucket-lifecycle worker started")
			return nil
		},
		OnStop: func(_ context.Context) error {
			if w.cancelTick != nil {
				w.cancelTick()
			}
			return nil
		},
	})
}

// DryRunHTTP exposes DryRun over HTTP. Query/body param `bucket`
// (optional) limits the preview to a single bucket.
//
//	@Summary		Preview the next bucket-lifecycle sweep
//	@Description	Return the set of objects the next bucket-lifecycle sweep would soft-delete, without acting. Used by operators to validate a policy before flipping blob.lifecycle.enabled.
//	@Tags			Blob
//	@ID				blob_lifecycle_dry_run
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket	query		string										false	"Limit preview to a single bucket"
//	@Success		200		{object}	dto.GenericResponse[LifecycleSweepResult]	"Preview"
//	@Failure		401		{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		404		{object}	dto.GenericResponse[any]					"Bucket not found"
//	@Failure		500		{object}	dto.GenericResponse[any]					"Internal server error"
//	@Router			/api/v2/blob/lifecycle/dry-run [get]
//	@x-api-type		{"portal":"true","sdk":"true","admin":"true"}
func (w *BucketLifecycleWorker) DryRunHTTP(c *gin.Context) {
	bucket := c.Query("bucket")
	res, err := w.DryRun(c.Request.Context(), bucket)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			dto.ErrorResponse(c, http.StatusNotFound, err.Error())
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	dto.SuccessResponse(c, res)
}

func (w *BucketLifecycleWorker) loop(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := w.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logrus.WithError(err).Warn("blob: bucket-lifecycle sweep iteration failed")
			}
		}
	}
}
