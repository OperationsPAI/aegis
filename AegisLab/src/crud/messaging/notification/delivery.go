package notification

import (
	"context"

	"gorm.io/gorm"
)

// DeliveryLogger records every Recipient×Channel attempt. v1 writes
// one row per attempt; a future v2 retry queue will read pending rows
// from here and re-attempt with exponential backoff. Kept behind an
// interface so we can swap the default DB-backed implementation for a
// no-op in tests or a streaming one in prod.
type DeliveryLogger interface {
	Record(ctx context.Context, attempt DeliveryAttempt) error
}

type DeliveryAttempt struct {
	NotificationID int64
	UserID         int
	Channel        ChannelKey
	Status         DeliveryStatus
	Attempt        int
	Error          string
}

type DBDeliveryLogger struct {
	DB *gorm.DB
}

func NewDBDeliveryLogger(db *gorm.DB) *DBDeliveryLogger { return &DBDeliveryLogger{DB: db} }

func (l *DBDeliveryLogger) Record(ctx context.Context, attempt DeliveryAttempt) error {
	row := NotificationDelivery{
		NotificationID: attempt.NotificationID,
		UserID:         attempt.UserID,
		Channel:        string(attempt.Channel),
		Status:         string(attempt.Status),
		Attempt:        attempt.Attempt,
		Error:          attempt.Error,
	}
	if row.Attempt == 0 {
		row.Attempt = 1
	}
	return l.DB.WithContext(ctx).Create(&row).Error
}

// NopDeliveryLogger is what tests pin when they don't care about the
// audit trail. Also useful behind a feature flag if the deliveries
// table ever becomes a write bottleneck.
type NopDeliveryLogger struct{}

func (NopDeliveryLogger) Record(_ context.Context, _ DeliveryAttempt) error { return nil }
