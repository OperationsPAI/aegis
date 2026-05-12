package notification

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// PreferenceEngine decides which channels a given recipient should be
// reached on for a given event. v1 default: inbox is always on; other
// channels look at the notification_subscriptions table.
//
// Why an interface from day 1: when we add quiet hours / per-workspace
// preferences / digest opt-in, all of that can drop into a richer
// implementation without producers learning about the new fields.
type PreferenceEngine interface {
	Route(ctx context.Context, userID int, evt *Event) ([]ChannelKey, error)
}

// DefaultPreferenceEngine is DB-backed. The inbox channel is forced on
// (it's the audit/history layer and cannot be opted out without losing
// the user's record of events). All other channels are default-on but
// can be disabled per-(user, category, channel) via the
// notification_subscriptions table.
type DefaultPreferenceEngine struct {
	DB *gorm.DB

	// EnabledChannels is the set the engine considers. v1 ships with
	// just inbox so the stub channels don't accidentally produce
	// no-op deliveries. As channels become real, append here.
	EnabledChannels []ChannelKey
}

func NewDefaultPreferenceEngine(db *gorm.DB, enabled []ChannelKey) *DefaultPreferenceEngine {
	if len(enabled) == 0 {
		enabled = []ChannelKey{ChannelInbox}
	}
	return &DefaultPreferenceEngine{DB: db, EnabledChannels: enabled}
}

func (e *DefaultPreferenceEngine) Route(ctx context.Context, userID int, evt *Event) ([]ChannelKey, error) {
	out := make([]ChannelKey, 0, len(e.EnabledChannels))
	for _, ch := range e.EnabledChannels {
		if ch == ChannelInbox {
			out = append(out, ch)
			continue
		}
		ok, err := e.isEnabled(ctx, userID, evt.Category, ch)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, ch)
		}
	}
	return out, nil
}

func (e *DefaultPreferenceEngine) isEnabled(ctx context.Context, userID int, category string, channel ChannelKey) (bool, error) {
	var sub NotificationSubscription
	err := e.DB.WithContext(ctx).
		Where("user_id = ? AND category = ? AND channel = ?", userID, category, string(channel)).
		First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Default-on for unseen (user, category, channel) tuples.
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return sub.Enabled, nil
}

// SubscriptionRepository is the read/write surface for the
// preferences UI. Kept separate from the engine so the engine can be
// swapped (e.g. for an LRU-cached one) without touching the handler.
type SubscriptionRepository struct {
	DB *gorm.DB
}

func NewSubscriptionRepository(db *gorm.DB) *SubscriptionRepository {
	return &SubscriptionRepository{DB: db}
}

func (r *SubscriptionRepository) ListForUser(ctx context.Context, userID int) ([]NotificationSubscription, error) {
	var subs []NotificationSubscription
	if err := r.DB.WithContext(ctx).Where("user_id = ?", userID).Find(&subs).Error; err != nil {
		return nil, err
	}
	return subs, nil
}

func (r *SubscriptionRepository) Set(ctx context.Context, sub NotificationSubscription) error {
	return r.DB.WithContext(ctx).
		Where("user_id = ? AND category = ? AND channel = ?",
			sub.UserID, sub.Category, sub.Channel).
		Assign(map[string]any{"enabled": sub.Enabled}).
		FirstOrCreate(&sub).Error
}
