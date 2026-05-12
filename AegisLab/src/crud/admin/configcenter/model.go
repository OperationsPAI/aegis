package configcenter

import "time"

// ConfigAudit is the durable log of every Set/Delete that the admin
// handler performed. The in-process Center.Set is internal-only; only
// the HTTP handler writes audit rows (per RFC).
type ConfigAudit struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	Namespace  string    `gorm:"not null;size:64;index:idx_ns_key,priority:1"`
	KeyPath    string    `gorm:"not null;size:256;index:idx_ns_key,priority:2"`
	Action     string    `gorm:"not null;size:16"`
	OldValue   []byte    `gorm:"type:json;serializer:json"`
	NewValue   []byte    `gorm:"type:json;serializer:json"`
	ActorID    *int      `gorm:"index"`
	ActorToken string    `gorm:"size:64"`
	Reason     string    `gorm:"size:256"`
	CreatedAt  time.Time `gorm:"autoCreateTime;index:idx_ns_key,priority:3,sort:desc"`
}

func (ConfigAudit) TableName() string { return "config_audit" }
