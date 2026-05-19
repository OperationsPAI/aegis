// Package chaoshooks implements the aegis-backend receivers for the
// aegis-chaos webhooks defined in aegislab/docs/aegis-chaos-design.md
// §10.2 (ADRs 0001/0002/0005/0006/0007).
package chaoshooks

import "time"

// HookSubmission is the receiver-side uniqueness gate. Per design §11
// step 4 the downstream submission must be idempotent on
// `(injection_id, task_type)` so that the legacy CRD watcher and the
// new aegis-chaos webhook cannot both fire BuildDatapack for the same
// fault. Since each `Kind` ("singleton"/"batch") maps to exactly one
// downstream task type (BuildDatapack), the PK `(id, kind)` is the
// concrete encoding of that constraint. `TerminalStatus` stays as a
// non-key column for audit — but a different terminal arriving later
// for the same (id, kind) is a no-op, not a second fire.
type HookSubmission struct {
	ID             string    `gorm:"primaryKey;size:64"`
	Kind           string    `gorm:"primaryKey;size:16"`
	TerminalStatus string    `gorm:"size:16;not null"`
	SubmittedAt    time.Time `gorm:"not null"`
}

func (HookSubmission) TableName() string { return "chaos_hook_submissions" }
