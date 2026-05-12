package notification

import (
	"context"
	"encoding/json"
	"fmt"

	redisinfra "aegis/platform/redis"
)

// InboxChannel is the only real channel in v1. It does two things:
//
//  1. Persist a `Notification` row (the source of truth — `GET
//     /notifications` reads from here, the bell counts from here,
//     `markRead` writes here).
//  2. Fan out a lightweight pub/sub event so any live SSE connection
//     for this user prepends the new row without polling.
//
// The DB write happens before the pub/sub so a slow/down Redis can't
// drop the persistent record. Pub/sub failures are logged and
// swallowed.
type InboxChannel struct {
	repo  *InboxRepository
	redis *redisinfra.Gateway
}

func NewInboxChannel(repo *InboxRepository, redis *redisinfra.Gateway) *InboxChannel {
	return &InboxChannel{repo: repo, redis: redis}
}

func (c *InboxChannel) Key() ChannelKey { return ChannelInbox }

// Deliver expects that the orchestrator has already inserted the
// Notification row (so all subsequent channels can attach to a stable
// id) and just does the live push. See Publisher.Publish.
func (c *InboxChannel) Deliver(ctx context.Context, target *DeliveryTarget) error {
	if target.NotificationID == 0 {
		return fmt.Errorf("inbox deliver: missing NotificationID")
	}
	if c.redis == nil {
		return nil
	}
	body, err := json.Marshal(map[string]any{
		"id":             target.NotificationID,
		"title":          target.Content.Title,
		"body":           target.Content.Body,
		"category":       target.Event.Category,
		"severity":       string(target.Event.Severity),
		"link_to":        target.Event.LinkTo,
		"notification":   "new",
	})
	if err != nil {
		return fmt.Errorf("encode pub/sub payload: %w", err)
	}
	if err := c.redis.Publish(ctx, inboxChannelKey(target.UserID), string(body)); err != nil {
		return fmt.Errorf("publish inbox event: %w", err)
	}
	return nil
}

// inboxChannelKey is the redis pub/sub key for one user's live feed.
// Stable across restarts so reconnecting SSE clients land in the same
// bucket without coordination.
func inboxChannelKey(userID int) string {
	return fmt.Sprintf("notifications:user:%d", userID)
}
