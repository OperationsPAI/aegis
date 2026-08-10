package chaos

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"aegis/crud/chaos/chaosmeta"
)

type chaosPointStore struct {
	db *gorm.DB
}

func NewChaosPointStore(db *gorm.DB) chaosmeta.ChaosPointStore {
	return &chaosPointStore{db: db}
}

func (s *chaosPointStore) QueryPoints(ctx context.Context, system string) ([]chaosmeta.ChaosPointRow, error) {
	var rows []Point
	if err := s.db.WithContext(ctx).
		Where("system_name = ? AND status = ?", system, PointActive).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("chaosPointStore: query chaos_points for system %q: %w", system, err)
	}
	out := make([]chaosmeta.ChaosPointRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, chaosmeta.ChaosPointRow{
			SystemName:     r.SystemName,
			CapabilityName: r.CapabilityName,
			Target:         map[string]any(r.Target),
		})
	}
	return out, nil
}

// LatestUpdate returns MAX(updated_at) across every status. Only soft-delete
// via the `status` column bumps updated_at — a hard `DELETE` would not move
// MAX and the probe would miss it, retaining the removed row in-cache. Soft
// delete is the chaos_points contract; raw DELETE is not a supported
// operator workflow.
//
// The scan target is `any` rather than `sql.NullTime` because the SQLite
// driver used in tests (gorm.io/driver/sqlite → mattn/go-sqlite3) returns
// aggregated DATETIME as []byte, not time.Time, and NullTime rejects that.
// MySQL returns time.Time natively. nullableTime handles both.
func (s *chaosPointStore) LatestUpdate(ctx context.Context, system string) (time.Time, error) {
	row := s.db.WithContext(ctx).
		Model(&Point{}).
		Where("system_name = ?", system).
		Select("MAX(updated_at)").
		Row()
	var ts nullableTime
	if err := row.Scan(&ts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("chaosPointStore: probe MAX(updated_at) for system %q: %w", system, err)
	}
	if !ts.Valid {
		return time.Time{}, nil
	}
	return ts.Time.UTC(), nil
}

// nullableTime accepts either a time.Time (MySQL driver) or a textual
// timestamp (mattn SQLite driver returns []byte for DATETIME aggregates).
type nullableTime struct {
	Time  time.Time
	Valid bool
}

func (n *nullableTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		return nil
	case time.Time:
		n.Time, n.Valid = v, true
		return nil
	case []byte:
		return n.parseString(string(v))
	case string:
		return n.parseString(v)
	}
	return fmt.Errorf("nullableTime: unsupported scan source %T", src)
}

func (n *nullableTime) parseString(s string) error {
	if s == "" {
		return nil
	}
	// SQLite's stock DATETIME serialisation; one format is enough since
	// MySQL hits the time.Time branch above.
	t, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", s)
	if err != nil {
		return fmt.Errorf("nullableTime: parse %q: %w", s, err)
	}
	n.Time, n.Valid = t, true
	return nil
}

func RegisterChaosPointStore(db *gorm.DB) {
	chaosmeta.SetChaosPointStore(NewChaosPointStore(db))
}
