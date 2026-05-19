// Package chaoshooks implements the aegis-backend receivers for
// aegis-chaos webhooks (`POST /api/v1/hooks/chaos` singleton +
// `POST /api/v1/hooks/chaos-batch`). Dead code until migration step 5b
// flips the per-system etcd flag. See aegislab/docs/aegis-chaos-design.md
// §10.2 and ADRs 0001/0002/0005/0006/0007.
package chaoshooks

import "time"

// HookSubmission is the receiver-side uniqueness gate. Per ADR-0007 and
// design §10.2 the backend's downstream submission must be idempotent on
// `(injection_or_batch_id, kind, terminal_status)`; duplicate webhooks
// (retry, polling, or shadowed CRD watcher during 5b) become no-ops.
//
// `Kind` is "singleton" or "batch". `TerminalStatus` is the sticky
// terminal value the webhook arrived with (succeeded/failed/cancelled for
// singleton; succeeded/partial/failed/cancelled for batch — ADR-0006).
type HookSubmission struct {
	ID             string    `gorm:"primaryKey;size:64"`
	Kind           string    `gorm:"primaryKey;size:16"`
	TerminalStatus string    `gorm:"primaryKey;size:16"`
	SubmittedAt    time.Time `gorm:"not null"`
}

func (HookSubmission) TableName() string { return "chaos_hook_submissions" }
