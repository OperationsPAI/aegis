package notification

import (
	"context"
	"fmt"
)

// Channel is the delivery sink for a single (user, content) pair. v1
// has one real implementation — InboxChannel — and three stubs (email
// / slack / webhook) wired but disabled by default. Adding a real
// email channel later is "drop in a new Channel implementation +
// enable in DefaultPreferenceEngine.EnabledChannels"; producers
// don't change.
type Channel interface {
	Key() ChannelKey
	// Deliver attempts a single delivery. Returning
	// ErrChannelNotConfigured / ErrChannelDisabled tells the publisher
	// to record a `skipped` delivery instead of `failed` — useful so
	// the operator dashboard distinguishes "send-failure" from
	// "intentionally not wired yet".
	Deliver(ctx context.Context, target *DeliveryTarget) error
}

// DeliveryTarget bundles everything a Channel needs to send: the
// rendered content, the user it's bound for, and (for channels that
// care) the original event metadata. Channels treat it read-only.
type DeliveryTarget struct {
	UserID         int
	NotificationID int64 // 0 when no inbox row was created (service-only events)
	Content        *RenderedContent
	Event          *Event
}

// Registry collects all Channel implementations and lets the publisher
// look them up by key. Keeping this as an indirection (instead of a
// `map[ChannelKey]Channel` literal in the publisher) means fx can
// inject channels via group:"channels" — every channel package adds
// itself to the registry without the publisher knowing about it.
type Registry struct {
	channels map[ChannelKey]Channel
}

func NewRegistry(channels []Channel) *Registry {
	m := make(map[ChannelKey]Channel, len(channels))
	for _, c := range channels {
		if c == nil {
			continue
		}
		m[c.Key()] = c
	}
	return &Registry{channels: m}
}

func (r *Registry) Get(key ChannelKey) (Channel, error) {
	c, ok := r.channels[key]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrChannelNotConfigured, key)
	}
	return c, nil
}

func (r *Registry) Keys() []ChannelKey {
	keys := make([]ChannelKey, 0, len(r.channels))
	for k := range r.channels {
		keys = append(keys, k)
	}
	return keys
}
