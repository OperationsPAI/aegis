// Package chaoshooks implements the aegis-backend receivers for the
// aegis-chaos webhooks defined in aegislab/docs/aegis-chaos-design.md
// §10.2 (ADRs 0001/0002/0005/0006/0007).
package chaoshooks

import "time"

// HookSubmission is the receiver-side uniqueness gate. Per ADR-0007 +
// design §10.2 the downstream submission must be idempotent on
// `(id, kind, terminal_status)` so that duplicate webhook deliveries
// (retry, polling, shadowed CRD watcher) become no-ops. `Kind` is
// "singleton" or "batch"; `TerminalStatus` is the sticky terminal value
// the webhook arrived with (ADR-0006).
type HookSubmission struct {
	ID             string    `gorm:"primaryKey;size:64"`
	Kind           string    `gorm:"primaryKey;size:16"`
	TerminalStatus string    `gorm:"primaryKey;size:16"`
	SubmittedAt    time.Time `gorm:"not null"`
}

func (HookSubmission) TableName() string { return "chaos_hook_submissions" }
