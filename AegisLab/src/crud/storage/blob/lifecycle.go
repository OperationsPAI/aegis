package blob

import (
	"context"
	"errors"
	"time"

	"aegis/platform/config"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

// DeletionWorker runs the periodic blob retention sweep:
//
//   - expires_at <= now (and not soft-deleted) → soft-delete the row.
//   - deleted_at IS NOT NULL                  → driver.Delete + hard
//     delete the row.
//   - size_bytes = 0 AND etag = '' AND created_at < now-orphanAge →
//     reconcile against driver.Stat; backfill size/etag if the upload
//     actually completed, otherwise soft-delete the orphan presign.
//
// One sweep iteration processes up to `batch` rows per pass; the
// fx.Lifecycle hook drives Run on a `interval` ticker.
type DeletionWorker struct {
	svc        *Service
	repo       *Repository
	registry   *Registry
	clock      Clock
	interval   time.Duration
	batch      int
	orphanAge  time.Duration
	disabled   bool
	cancelTick context.CancelFunc
}

// DeletionWorkerConfig captures the knobs the lifecycle worker reads
// from `[blob.lifecycle]`. Zero values fall back to safe defaults.
type DeletionWorkerConfig struct {
	Interval  time.Duration
	Batch     int
	OrphanAge time.Duration
	Disabled  bool
}

func parseDurationOrZero(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

func loadDeletionWorkerConfig() DeletionWorkerConfig {
	cfg := DeletionWorkerConfig{
		Interval:  parseDurationOrZero(config.GetString("blob.lifecycle.interval")),
		Batch:     config.GetInt("blob.lifecycle.batch"),
		OrphanAge: parseDurationOrZero(config.GetString("blob.lifecycle.orphan_age")),
		Disabled:  config.GetBool("blob.lifecycle.disabled"),
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 200
	}
	if cfg.OrphanAge <= 0 {
		cfg.OrphanAge = time.Hour
	}
	return cfg
}

func NewDeletionWorker(svc *Service, repo *Repository, reg *Registry, clock Clock) *DeletionWorker {
	cfg := loadDeletionWorkerConfig()
	return &DeletionWorker{
		svc:       svc,
		repo:      repo,
		registry:  reg,
		clock:     clock,
		interval:  cfg.Interval,
		batch:     cfg.Batch,
		orphanAge: cfg.OrphanAge,
		disabled:  cfg.Disabled,
	}
}

// Run executes one sweep iteration. Errors on individual rows are
// logged and accumulated but do not abort the sweep — a single broken
// driver call should not stall the rest of the queue.
func (w *DeletionWorker) Run(ctx context.Context) error {
	now := w.clock.Now()
	w.expirePass(ctx, now)
	w.reconcilePass(ctx, now)
	w.deletePass(ctx)
	return nil
}

func (w *DeletionWorker) expirePass(ctx context.Context, now time.Time) {
	rows, err := w.repo.ListExpired(ctx, now, w.batch)
	if err != nil {
		logrus.WithError(err).Warn("blob: lifecycle list-expired failed")
		return
	}
	for _, r := range rows {
		if err := w.repo.MarkExpired(ctx, r.ID, now); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{"bucket": r.Bucket, "key": r.StorageKey}).
				Warn("blob: lifecycle mark-expired failed")
		}
	}
	if len(rows) > 0 {
		logrus.WithField("count", len(rows)).Info("blob: lifecycle expired rows soft-deleted")
	}
}

func (w *DeletionWorker) deletePass(ctx context.Context) {
	rows, err := w.repo.ListSoftDeleted(ctx, w.batch)
	if err != nil {
		logrus.WithError(err).Warn("blob: lifecycle list-soft-deleted failed")
		return
	}
	var purged int
	for _, r := range rows {
		bkt, err := w.registry.Lookup(r.Bucket)
		if err != nil {
			// Bucket no longer configured — skip the driver call but
			// also leave the row alone (operator may want to inspect).
			logrus.WithError(err).WithField("bucket", r.Bucket).
				Warn("blob: lifecycle skipping row for unknown bucket")
			continue
		}
		if derr := bkt.Driver.Delete(ctx, r.StorageKey); derr != nil && !errors.Is(derr, ErrObjectNotFound) {
			logrus.WithError(derr).WithFields(logrus.Fields{"bucket": r.Bucket, "key": r.StorageKey}).
				Warn("blob: lifecycle driver-delete failed")
			continue
		}
		if err := w.repo.HardDelete(ctx, r.ID); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{"bucket": r.Bucket, "key": r.StorageKey}).
				Warn("blob: lifecycle hard-delete failed")
			continue
		}
		purged++
	}
	if purged > 0 {
		logrus.WithField("count", purged).Info("blob: lifecycle purged soft-deleted rows")
	}
}

func (w *DeletionWorker) reconcilePass(ctx context.Context, now time.Time) {
	cutoff := now.Add(-w.orphanAge)
	rows, err := w.repo.ListOrphanCandidates(ctx, cutoff, w.batch)
	if err != nil {
		logrus.WithError(err).Warn("blob: lifecycle list-orphans failed")
		return
	}
	var backfilled, abandoned int
	for _, r := range rows {
		bkt, err := w.registry.Lookup(r.Bucket)
		if err != nil {
			continue
		}
		meta, serr := bkt.Driver.Stat(ctx, r.StorageKey)
		if serr != nil {
			if errors.Is(serr, ErrObjectNotFound) {
				if err := w.repo.MarkExpired(ctx, r.ID, now); err == nil {
					abandoned++
				}
				continue
			}
			logrus.WithError(serr).WithFields(logrus.Fields{"bucket": r.Bucket, "key": r.StorageKey}).
				Warn("blob: lifecycle stat failed")
			continue
		}
		if err := w.repo.MarkUploaded(ctx, r.ID, meta.Size, meta.ETag); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{"bucket": r.Bucket, "key": r.StorageKey}).
				Warn("blob: lifecycle backfill failed")
			continue
		}
		backfilled++
	}
	if backfilled > 0 || abandoned > 0 {
		logrus.WithFields(logrus.Fields{"backfilled": backfilled, "abandoned": abandoned}).
			Info("blob: lifecycle reconcile pass")
	}
}

// registerLifecycle starts the periodic sweep on fx start and shuts it
// down on fx stop. Disabled if `[blob.lifecycle] disabled = true` so
// short-lived CLI / migration binaries don't spawn the goroutine.
func registerLifecycle(lc fx.Lifecycle, w *DeletionWorker) {
	if w.disabled {
		logrus.Info("blob: lifecycle worker disabled by config")
		return
	}
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			ctx, cancel := context.WithCancel(context.Background())
			w.cancelTick = cancel
			go w.loop(ctx)
			logrus.WithField("interval", w.interval).Info("blob: lifecycle worker started")
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

func (w *DeletionWorker) loop(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.Run(ctx); err != nil {
				logrus.WithError(err).Warn("blob: lifecycle sweep iteration failed")
			}
		}
	}
}
