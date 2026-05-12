// Package notificationclient is the producer-side surface for emitting
// notifications. Producers (module/injection, module/dataset,
// module/auth's api-key expiry job, etc.) depend ONLY on this package.
//
// Two interchangeable implementations are wired via fx:
//
//   - Local — calls the in-process notification.Publisher. Used when
//     the notification logic runs in the same binary as the producers
//     (today's aegis-backend monolith).
//   - Remote — POSTs to the notification microservice's
//     `/api/v2/events:publish` endpoint over HTTP, authenticated with a
//     service token from SSO's client_credentials grant. Used once the
//     `aegis-notify` binary is split out.
//
// Switching deployment mode is a config flip ([notification] mode =
// "local"|"remote") — no producer code changes.
package notificationclient

import "context"

// Client is the only type producers reference. The signature mirrors
// notification.PublishReq's wire shape so producers in either mode
// pass the same struct.
type Client interface {
	Publish(ctx context.Context, req PublishReq) (*PublishResult, error)
}

// PublishReq is the producer-facing payload. Kept simple — JSON-ish
// fields, no internal types. The local impl maps it to
// notification.Event; the remote impl POSTs it as JSON.
type PublishReq struct {
	Category    string         `json:"category"`
	Severity    string         `json:"severity,omitempty"`
	Title       string         `json:"title"`
	Body        string         `json:"body,omitempty"`
	LinkTo      string         `json:"link_to,omitempty"`
	ActorUserID *int           `json:"actor_user_id,omitempty"`
	ActorName   string         `json:"actor_name,omitempty"`
	EntityKind  string         `json:"entity_kind,omitempty"`
	EntityID    string         `json:"entity_id,omitempty"`
	Payload     map[string]any `json:"payload,omitempty"`
	DedupeKey   string         `json:"dedupe_key,omitempty"`
	// Recipients
	UserIDs []int    `json:"user_ids,omitempty"`
	Roles   []string `json:"roles,omitempty"`
}

type PublishResult struct {
	NotificationIDs []string `json:"notification_ids"`
	DroppedDedupe   bool     `json:"dropped_dedupe,omitempty"`
}
