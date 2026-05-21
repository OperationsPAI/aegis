package chaos

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"aegis/internal/chaosengine/chaosmeta"
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

func RegisterChaosPointStore(db *gorm.DB) {
	chaosmeta.SetChaosPointStore(NewChaosPointStore(db))
}
