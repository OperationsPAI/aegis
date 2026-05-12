package notification

import (
	"encoding/json"
	"strconv"

	"github.com/sirupsen/logrus"
)

// InboxItem is the wire shape sent to the console. It mirrors
// aegis-ui's `AegisNotification` 1:1 so the frontend needs no adapter
// layer.
type InboxItem struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Body      string         `json:"body,omitempty"`
	Timestamp string         `json:"timestamp"`
	Read      bool           `json:"read"`
	To        string         `json:"to,omitempty"`
	Category  string         `json:"category,omitempty"`
	Severity  string         `json:"severity,omitempty"`
	Actor     string         `json:"actor,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

func ToInboxItem(n *Notification) InboxItem {
	item := InboxItem{
		ID:        strconv.FormatInt(n.ID, 10),
		Title:     n.Title,
		Body:      n.Body,
		Timestamp: n.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		Read:      n.ReadAt != nil,
		To:        n.LinkTo,
		Category:  n.Category,
		Severity:  n.Severity,
		Actor:     n.ActorName,
	}
	if len(n.Payload) > 0 {
		var p map[string]any
		if err := json.Unmarshal(n.Payload, &p); err != nil {
			logrus.WithError(err).WithField("notification_id", n.ID).
				Debug("inbox payload not valid json; dropping for response")
		} else {
			item.Payload = p
		}
	}
	return item
}

type ListInboxResp struct {
	Items       []InboxItem `json:"items"`
	NextCursor  string      `json:"next_cursor,omitempty"`
	UnreadCount int64       `json:"unread_count"`
}

type UnreadCountResp struct {
	UnreadCount int64 `json:"unread_count"`
}

// PublishEventReq is the wire shape the cross-service ingestion
// endpoint accepts. It deliberately mirrors the in-process `Event`
// without exposing internal types like ChannelKey (the routing
// happens inside the service, not at the wire).
type PublishEventReq struct {
	Category    string         `json:"category" binding:"required"`
	Severity    string         `json:"severity,omitempty"`
	Title       string         `json:"title" binding:"required"`
	Body        string         `json:"body,omitempty"`
	LinkTo      string         `json:"link_to,omitempty"`
	ActorUserID *int           `json:"actor_user_id,omitempty"`
	ActorName   string         `json:"actor_name,omitempty"`
	EntityKind  string         `json:"entity_kind,omitempty"`
	EntityID    string         `json:"entity_id,omitempty"`
	Payload     map[string]any `json:"payload,omitempty"`
	DedupeKey   string         `json:"dedupe_key,omitempty"`
	UserIDs     []int          `json:"user_ids,omitempty"`
	Roles       []string       `json:"roles,omitempty"`
}

func (r *PublishEventReq) ToEvent() *Event {
	sev := Severity(r.Severity)
	if sev == "" {
		sev = SeverityInfo
	}
	evt := &Event{
		Category:    r.Category,
		Severity:    sev,
		Title:       r.Title,
		Body:        r.Body,
		LinkTo:      r.LinkTo,
		ActorUserID: r.ActorUserID,
		EntityKind:  r.EntityKind,
		EntityID:    r.EntityID,
		Payload:     r.Payload,
		DedupeKey:   r.DedupeKey,
	}
	for _, u := range r.UserIDs {
		evt.Recipients = append(evt.Recipients, Recipient{Kind: RecipientUser, UserID: u})
	}
	for _, role := range r.Roles {
		evt.Recipients = append(evt.Recipients, Recipient{Kind: RecipientRole, RoleName: role})
	}
	return evt
}

type PublishEventResp struct {
	NotificationIDs []string                 `json:"notification_ids"`
	Deliveries      []DeliveryAttemptSummary `json:"deliveries,omitempty"`
	DroppedDedupe   bool                     `json:"dropped_dedupe,omitempty"`
}

func PublishResultToResp(res *PublishResult) PublishEventResp {
	ids := make([]string, len(res.NotificationIDs))
	for i, id := range res.NotificationIDs {
		ids[i] = strconv.FormatInt(id, 10)
	}
	return PublishEventResp{
		NotificationIDs: ids,
		Deliveries:      res.Deliveries,
		DroppedDedupe:   res.DroppedDedupe,
	}
}

type SetSubscriptionReq struct {
	Category string `json:"category" binding:"required"`
	Channel  string `json:"channel" binding:"required"`
	Enabled  bool   `json:"enabled"`
}

type SubscriptionResp struct {
	Category string `json:"category"`
	Channel  string `json:"channel"`
	Enabled  bool   `json:"enabled"`
}
