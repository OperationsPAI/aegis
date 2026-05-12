package model

import "time"

// Status values for SystemPrerequisite.Status. Strings (rather than an int
// enum) so the column is self-documenting when inspected via `SELECT *`.
const (
	SystemPrerequisiteStatusPending    = "pending"
	SystemPrerequisiteStatusReconciled = "reconciled"
	SystemPrerequisiteStatusFailed     = "failed"
)

// Kind values for SystemPrerequisite.Kind. Only "helm" is supported today;
// new kinds (operator bundle / kubectl-apply / script) can be added without a
// schema change because the per-kind payload lives in SpecJSON.
const (
	SystemPrerequisiteKindHelm = "helm"
)

// SystemPrerequisite declares a cluster-level dependency that must be present
// before a given benchmark system can be enabled (issue #115).
//
// The benchmark "system" is identified by SystemName — not a FK to a systems
// table, because that table was retired in issue #75 (systems now live in etcd
// + dynamic_configs, keyed by name). Unique key (system_name, kind, name)
// keeps the seed loader idempotent: re-running seeding never duplicates a row,
// and status/spec updates flow through ON CONFLICT.
//
// SpecJSON carries the per-kind payload. For kind="helm":
//
//	{"chart": "coherence/coherence-operator",
//	 "namespace": "coherence-test",
//	 "version": ">=3.4",
//	 "values": [{"key":"image.registry","value":"pair-cn-shanghai.cr.volces.com/opspai"}]}
type SystemPrerequisite struct {
	ID         int       `gorm:"primaryKey;autoIncrement" json:"id"`
	SystemName string    `gorm:"not null;size:128;uniqueIndex:idx_sysprereq,priority:1" json:"system_name"`
	Kind       string    `gorm:"not null;size:32;uniqueIndex:idx_sysprereq,priority:2" json:"kind"`
	Name       string    `gorm:"not null;size:128;uniqueIndex:idx_sysprereq,priority:3" json:"name"`
	SpecJSON   string    `gorm:"type:json;not null" json:"spec_json"`
	Status     string    `gorm:"not null;size:32;default:pending" json:"status"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (SystemPrerequisite) TableName() string { return "system_prerequisites" }
