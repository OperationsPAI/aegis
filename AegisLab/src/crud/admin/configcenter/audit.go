package configcenter

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"
)

// AuditWriter persists ConfigAudit rows. The HTTP handler calls it
// synchronously on every Set/Delete so the audit log is the source of
// truth for "who changed what".
type AuditWriter interface {
	Write(ctx context.Context, row ConfigAudit) error
}

type dbAuditWriter struct {
	db *gorm.DB
}

// NewDBAuditWriter writes audit rows to the application database.
func NewDBAuditWriter(db *gorm.DB) *dbAuditWriter {
	return &dbAuditWriter{db: db}
}

func (w *dbAuditWriter) Write(ctx context.Context, row ConfigAudit) error {
	return w.db.WithContext(ctx).Create(&row).Error
}

// auditValue marshals an arbitrary value into the JSON []byte the
// model expects (`type:json`). nil is encoded as nil, not `"null"`.
func auditValue(v any) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
