package chaos

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gorm.io/gorm"

	"aegis/crud/chaos/chaosmeta"
)

// chaos_points DB-read instrumentation. Histogram buckets cover the 1ms-1s
// log range; the byte-cluster `ts` system returns ~thousands of rows per
// SELECT and lands well under 100ms when MySQL is healthy. The rows
// histogram is sized to spot data drift (sudden drop after a bad supersede).
var (
	chaosPointsSelectSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "aegis_chaos_points_select_seconds",
		Help:    "MySQL roundtrip latency for chaos_points SELECT by system.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
	}, []string{"system"})

	chaosPointsRowsReturned = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "aegis_chaos_points_rows_returned",
		Help:    "Row count returned by chaos_points SELECT by system.",
		Buckets: []float64{1, 10, 100, 500, 1000, 5000, 10000},
	}, []string{"system"})
)

type chaosPointStore struct {
	db *gorm.DB
}

func NewChaosPointStore(db *gorm.DB) chaosmeta.ChaosPointStore {
	return &chaosPointStore{db: db}
}

func (s *chaosPointStore) QueryPoints(ctx context.Context, system string) ([]chaosmeta.ChaosPointRow, error) {
	timer := prometheus.NewTimer(chaosPointsSelectSeconds.WithLabelValues(system))
	var rows []Point
	err := s.db.WithContext(ctx).
		Where("system_name = ? AND status = ?", system, PointActive).
		Find(&rows).Error
	timer.ObserveDuration()
	if err != nil {
		return nil, fmt.Errorf("chaosPointStore: query chaos_points for system %q: %w", system, err)
	}
	chaosPointsRowsReturned.WithLabelValues(system).Observe(float64(len(rows)))
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

// timestampFormats covers MySQL's DATETIME (without TZ) and SQLite's stock
// DATETIME serialisation; production runs on MySQL but tests cover SQLite.
var timestampFormats = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

func (s *chaosPointStore) LatestUpdate(ctx context.Context, system string) (time.Time, error) {
	row := s.db.WithContext(ctx).
		Model(&Point{}).
		Where("system_name = ?", system).
		Select("MAX(updated_at)").
		Row()
	var raw sql.NullString
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("chaosPointStore: probe MAX(updated_at) for system %q: %w", system, err)
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, nil
	}
	for _, layout := range timestampFormats {
		if t, err := time.Parse(layout, raw.String); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("chaosPointStore: unparseable MAX(updated_at) %q for system %q", raw.String, system)
}

func RegisterChaosPointStore(db *gorm.DB) {
	chaosmeta.SetChaosPointStore(NewChaosPointStore(db))
}
