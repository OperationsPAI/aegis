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
}

func NewReconciler(db *gorm.DB, executor Executor, webhook *WebhookSender, interval time.Duration, logger *logrus.Logger) *Reconciler {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	return &Reconciler{db: db, executor: executor, webhook: webhook, interval: interval, logger: logger}
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
		if inj.Status == StatusPending && state == ExecStateRunning {
			now := time.Now().UTC()
			r.db.WithContext(ctx).Model(&Injection{}).Where("id = ? AND status = ?", inj.ID, StatusPending).
				Updates(map[string]any{"status": StatusRunning, "started_at": &now})
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
