package chaos

import (
	"context"
	"errors"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

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
		Limit(200).Find(&rows).Error; err != nil {
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
	if err := r.webhook.Fire(ctx, &fresh); err != nil && !errors.Is(err, errWebhookDisabled) {
		r.logger.WithError(err).WithField("id", inj.ID).Warn("chaos reconciler: webhook fire")
	}
}
