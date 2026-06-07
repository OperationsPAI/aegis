package chaos

import (
	"context"
	"errors"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// reconcilerBatchSize caps the per-tick scan so a slow Executor.Status
// call (or a slow webhook delivery downstream) can't starve the loop.
// Raise this when row throughput grows enough that 200/tick saturates
// — config plumbing for it is step 5b/5c work, not 5a.
const reconcilerBatchSize = 200

// stuckPendingGrace is added to an injection's expected completion time
// (start + inject duration) before a still-non-terminal CR is force-failed.
// A new round can reuse a prior round's content-hashed CR name; if that
// stale CR was applied with a selector that matched no pods it never flips
// AllInjected/AllRecovered and interpretChaosMeshStatus parks it at Pending
// forever (no CR-404, so the orphaned path never fires). Without this gate
// the row stays `running`, the completion webhook never fires, and the trace
// wedges at fault.injection.started. The grace is generous so a normal
// in-flight injection — whose CR legitimately sits Pending until its pods are
// selected and AllInjected lands — is never force-failed.
const stuckPendingGrace = 10 * time.Minute

// stuckPendingFlatTimeout is the fallback when the row carries no usable
// inject duration: force-fail a non-terminal CR this long after the
// injection started.
const stuckPendingFlatTimeout = 15 * time.Minute

// orphanApplyGrace is how long a freshly-created row may read as Orphaned
// (CR-404) before the reconciler treats it as a genuine vanished CR.
// CreateInjection INSERTs the row as `pending` before Apply creates the
// chaos-mesh CR; a tick landing in that in-request window must not force-fail
// a CR that hasn't been Applied yet. The window only needs to cover the Apply
// round-trip, so the grace is short.
const orphanApplyGrace = 30 * time.Second

// Reconciler closes ADR-0006's stickiness loop. The CreateInjection path
// flips the row to `running` after Apply but cannot observe the
// chaos-mesh CR moving to AllRecovered; without this polling loop the
// row stays `running` forever and the webhook never fires.
type Reconciler struct {
	db       *gorm.DB
	executor Executor
	webhook  *WebhookSender
	interval time.Duration
	logger   *logrus.Logger
	now      func() time.Time
}

func NewReconciler(db *gorm.DB, executor Executor, webhook *WebhookSender, interval time.Duration, logger *logrus.Logger) *Reconciler {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	return &Reconciler{db: db, executor: executor, webhook: webhook, interval: interval, logger: logger, now: func() time.Time { return time.Now().UTC() }}
}

func (r *Reconciler) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *Reconciler) tick(ctx context.Context) {
	var rows []Injection
	if err := r.db.WithContext(ctx).
		Where("status IN ?", []string{StatusPending, StatusRunning}).
		Limit(reconcilerBatchSize).Find(&rows).Error; err != nil {
		r.logger.WithError(err).Warn("chaos reconciler: list active injections")
		return
	}
	for i := range rows {
		r.reconcileOne(ctx, &rows[i])
	}
}

func (r *Reconciler) reconcileOne(ctx context.Context, inj *Injection) {
	state, diag, err := r.executor.Status(ctx, inj.ExecutorHandle)
	if err != nil {
		r.logger.WithError(err).WithField("id", inj.ID).Debug("chaos reconciler: executor status")
		return
	}
	if state == ExecStatePending || state == ExecStateRunning {
		if state == ExecStatePending && r.isStuckPending(inj) {
			r.forceFailStuck(ctx, inj)
			return
		}
		if inj.Status == StatusPending && state == ExecStateRunning {
			now := r.now()
			r.db.WithContext(ctx).Model(&Injection{}).Where("id = ? AND status = ?", inj.ID, StatusPending).
				Updates(map[string]any{"status": StatusRunning, "started_at": &now})
		}
		return
	}

	// Orphaned (CR vanished). If the row was still in-flight, a 404 means
	// someone (or the GC) destroyed the CR before chaos-mesh could report
	// AllInjected/AllRecovered. Mark failed with a clear reason so the
	// downstream pipeline doesn't treat a non-event as a successful inject.
	// If the row was already terminal pending->the legitimate post-Destroy
	// case, leave it alone (the stickiness gate below would no-op anyway).
	if state == ExecStateOrphaned {
		if inj.Status != StatusPending && inj.Status != StatusRunning {
			return
		}
		// CreateInjection persists the row as `pending` BEFORE Apply creates
		// the CR. A reconciler tick landing in that in-request window 404s on
		// a CR that simply doesn't exist yet — that is not a vanished CR.
		// Skip until the row is older than the apply grace; a genuine
		// long-orphaned CR still force-fails on a later tick.
		if r.isFreshlyApplying(inj) {
			return
		}
		now := time.Now().UTC()
		diag["reason"] = "cr_vanished_mid_flight"
		tx := r.db.WithContext(ctx).Model(&Injection{}).
			Where("id = ? AND status IN ?", inj.ID, []string{StatusPending, StatusRunning}).
			Updates(map[string]any{
				"status":      StatusFailed,
				"finished_at": &now,
				"diagnostics": JSONMap(diag),
			})
		if tx.Error != nil {
			r.logger.WithError(tx.Error).WithField("id", inj.ID).Warn("chaos reconciler: orphaned update")
			return
		}
		if tx.RowsAffected == 0 {
			return
		}
		if r.webhook == nil {
			return
		}
		var fresh Injection
		if err := r.db.WithContext(ctx).Where("id = ?", inj.ID).Take(&fresh).Error; err != nil {
			r.logger.WithError(err).WithField("id", inj.ID).Warn("chaos reconciler: reload row")
			return
		}
		if err := r.webhook.Fire(ctx, &fresh); err != nil && !errors.Is(err, errWebhookDisabled) {
			r.logger.WithError(err).WithField("id", inj.ID).Warn("chaos reconciler: webhook fire")
		}
		if fresh.BatchID != nil && *fresh.BatchID != "" {
			r.reconcileBatch(ctx, *fresh.BatchID)
		}
		return
	}

	terminal := StatusSucceeded
	if state == ExecStateFailed {
		terminal = StatusFailed
	}
	now := time.Now().UTC()

	// Stickiness gate: only the first transition writes. Re-check status
	// inside the conditional UPDATE; if another reconciler instance won,
	// RowsAffected is 0 and we skip the webhook.
	tx := r.db.WithContext(ctx).Model(&Injection{}).
		Where("id = ? AND status IN ?", inj.ID, []string{StatusPending, StatusRunning}).
		Updates(map[string]any{
			"status":      terminal,
			"finished_at": &now,
			"diagnostics": JSONMap(diag),
		})
	if tx.Error != nil {
		r.logger.WithError(tx.Error).WithField("id", inj.ID).Warn("chaos reconciler: terminal update")
		return
	}
	if tx.RowsAffected == 0 {
		return
	}

	if r.webhook == nil {
		return
	}
	var fresh Injection
	if err := r.db.WithContext(ctx).Where("id = ?", inj.ID).Take(&fresh).Error; err != nil {
		r.logger.WithError(err).WithField("id", inj.ID).Warn("chaos reconciler: reload row")
		return
	}
	// Fire blocks this single-goroutine reconciler for up to ~15s of retry
	// sleep + 5×60s of per-attempt timeout, so one stuck receiver stalls
	// every other row's reconcile. Acceptable while row throughput is low;
	// revisit (bounded goroutine pool or dedicated webhook worker queue)
	// when that ceases to hold.
	if err := r.webhook.Fire(ctx, &fresh); err != nil && !errors.Is(err, errWebhookDisabled) {
		r.logger.WithError(err).WithField("id", inj.ID).Warn("chaos reconciler: webhook fire")
	}

	if fresh.BatchID != nil && *fresh.BatchID != "" {
		r.reconcileBatch(ctx, *fresh.BatchID)
	}
}

// isStuckPending reports whether a non-terminal CR that Status still reads
// as ExecStatePending has sat there past its expected completion. The
// deadline is derived entirely from the row: the time the injection should
// have started running (started_at, falling back to ts) plus the inject
// duration carried in params plus a grace window. A row with no usable
// duration uses a conservative flat timeout instead. Returning false for a
// freshly-applied CR is what keeps a normal in-flight injection from being
// force-failed.
func (r *Reconciler) isStuckPending(inj *Injection) bool {
	start := inj.Ts
	if inj.StartedAt != nil {
		start = *inj.StartedAt
	}
	if start.IsZero() {
		return false
	}
	if d := injectDuration(inj.Params); d > 0 {
		return r.now().After(start.Add(d + stuckPendingGrace))
	}
	return r.now().After(start.Add(stuckPendingFlatTimeout))
}

// isFreshlyApplying reports whether the row was created so recently that its
// CR may not have been Applied yet, so an Orphaned (CR-404) read is the
// in-request window — not a vanished CR. A row with no usable timestamp is
// treated as not-fresh so a genuinely orphaned legacy row still force-fails.
func (r *Reconciler) isFreshlyApplying(inj *Injection) bool {
	start := inj.Ts
	if inj.StartedAt != nil {
		start = *inj.StartedAt
	}
	if start.IsZero() {
		return false
	}
	return r.now().Before(start.Add(orphanApplyGrace))
}

// injectDuration reads the `duration_s` knob the params payload carries and
// returns it as a Duration, mirroring durationFromParams' type handling.
// Zero means "no usable duration".
func injectDuration(params JSONMap) time.Duration {
	v, ok := params["duration_s"]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return time.Duration(n) * time.Second
	case int:
		return time.Duration(n) * time.Second
	case int64:
		return time.Duration(n) * time.Second
	}
	return 0
}

// forceFailStuck terminalizes a wedged Pending injection: status=failed with
// reason cr_never_injected, finished_at=now, and the failed completion
// webhook fired so aegis-api advances the trace to fault.injection.failed
// (and releases the namespace lock). Uses the same stickiness-gated UPDATE
// + reload + Fire path a normal terminal transition does.
func (r *Reconciler) forceFailStuck(ctx context.Context, inj *Injection) {
	now := r.now()
	diag := JSONMap{"reason": "cr_never_injected"}
	tx := r.db.WithContext(ctx).Model(&Injection{}).
		Where("id = ? AND status IN ?", inj.ID, []string{StatusPending, StatusRunning}).
		Updates(map[string]any{
			"status":      StatusFailed,
			"finished_at": &now,
			"diagnostics": diag,
		})
	if tx.Error != nil {
		r.logger.WithError(tx.Error).WithField("id", inj.ID).Warn("chaos reconciler: stuck force-fail update")
		return
	}
	if tx.RowsAffected == 0 {
		return
	}
	r.logger.WithField("id", inj.ID).Warn("chaos reconciler: force-failed CR stuck Pending past deadline")
	if r.webhook == nil {
		return
	}
	var fresh Injection
	if err := r.db.WithContext(ctx).Where("id = ?", inj.ID).Take(&fresh).Error; err != nil {
		r.logger.WithError(err).WithField("id", inj.ID).Warn("chaos reconciler: reload row")
		return
	}
	if err := r.webhook.Fire(ctx, &fresh); err != nil && !errors.Is(err, errWebhookDisabled) {
		r.logger.WithError(err).WithField("id", inj.ID).Warn("chaos reconciler: webhook fire")
	}
	if fresh.BatchID != nil && *fresh.BatchID != "" {
		r.reconcileBatch(ctx, *fresh.BatchID)
	}
}

// reconcileBatch recomputes aggregated_status from all children. The
// SELECT … FOR UPDATE acts as the stickiness gate per ADR-0006: only the
// first observer that sees all-children-terminal writes the terminal row,
// subsequent reconciler ticks see a terminal aggregated_status and bail.
func (r *Reconciler) reconcileBatch(ctx context.Context, batchID string) {
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		r.logger.WithError(tx.Error).WithField("batch_id", batchID).Warn("chaos reconciler: batch tx begin")
		return
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	var batch InjectionBatch
	q := tx.Where("id = ?", batchID)
	if tx.Dialector.Name() == "mysql" {
		q = q.Set("gorm:query_option", "FOR UPDATE")
	}
	if err := q.Take(&batch).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			r.logger.WithError(err).WithField("batch_id", batchID).Warn("chaos reconciler: load batch")
		}
		return
	}
	if isAggTerminal(batch.AggregatedStatus) {
		return
	}

	var children []Injection
	if err := tx.Where("batch_id = ?", batchID).Find(&children).Error; err != nil {
		r.logger.WithError(err).WithField("batch_id", batchID).Warn("chaos reconciler: load children")
		return
	}
	statuses := make([]string, 0, len(children))
	for _, c := range children {
		statuses = append(statuses, c.Status)
	}
	agg := ComputeAggregatedStatus(statuses)
	if agg == batch.AggregatedStatus && !isAggTerminal(agg) {
		return
	}

	updates := map[string]any{"aggregated_status": agg}
	now := time.Now().UTC()
	if isAggTerminal(agg) && batch.FinishedAt == nil {
		updates["finished_at"] = &now
		batch.FinishedAt = &now
	}
	if err := tx.Model(&InjectionBatch{}).Where("id = ? AND aggregated_status = ?", batchID, batch.AggregatedStatus).
		Updates(updates).Error; err != nil {
		r.logger.WithError(err).WithField("batch_id", batchID).Warn("chaos reconciler: batch update")
		return
	}
	if err := tx.Commit().Error; err != nil {
		r.logger.WithError(err).WithField("batch_id", batchID).Warn("chaos reconciler: batch commit")
		return
	}
	committed = true
	batch.AggregatedStatus = agg

	if !isAggTerminal(agg) || r.webhook == nil {
		return
	}
	if err := r.webhook.FireBatch(ctx, &batch, children); err != nil && !errors.Is(err, errWebhookDisabled) {
		r.logger.WithError(err).WithField("batch_id", batchID).Warn("chaos reconciler: batch webhook fire")
	}
}
