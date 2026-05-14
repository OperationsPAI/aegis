package notification

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Severity tracks the tone the UI uses to render an item. The set is
// intentionally narrow — it must round-trip 1:1 with the frontend's
// AegisNotification.severity enum.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeveritySuccess Severity = "success"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// ChannelKey identifies a delivery channel. Strings rather than ints so
// new channels can land without a global enum migration.
type ChannelKey string

const (
	ChannelInbox   ChannelKey = "inbox"
	ChannelEmail   ChannelKey = "email"
	ChannelSlack   ChannelKey = "slack"
	ChannelWebhook ChannelKey = "webhook"
)

// DeliveryStatus tracks the lifecycle of a single Recipient×Channel
// delivery attempt. Useful for retries and the future observability
// dashboard — v1 only writes Queued + Delivered/Failed.
type DeliveryStatus string

const (
	DeliveryQueued    DeliveryStatus = "queued"
	DeliveryDelivered DeliveryStatus = "delivered"
	DeliveryFailed    DeliveryStatus = "failed"
	DeliverySkipped   DeliveryStatus = "skipped"
)

// Event is what a producer publishes. The intent is "X happened",
// not "send Alice a message". RecipientResolver translates Event →
// []Recipient downstream.
//
// Required: Category. Everything else is optional, but a meaningful
// Title is strongly recommended (the templater falls back to "<empty>"
// otherwise).
type Event struct {
	// Stable dotted key. Producer owns the namespace, e.g.
	// "injection.completed", "dataset.build.failed".
	Category string

	// info|success|warning|error. Defaults to SeverityInfo.
	Severity Severity

	// Channel-agnostic title and body. Templates can override per
	// channel later; for v1 these are used verbatim.
	Title string
	Body  string

	// Optional deep link the UI navigates to when the recipient clicks
	// the notification. Must be a relative path (`/portal/...`) — the
	// frontend prepends its origin.
	LinkTo string

	// Who/what caused the event. ActorUserID populates the
	// `actor` field in AegisNotification (denormalised at publish
	// time so the inbox endpoint is single-query).
	ActorUserID *int

	// Subject of the event in domain terms. EntityKind + EntityID let
	// the resolver fanout to "anyone watching this entity" and let the
	// producer dedupe ("already notified about this injection").
	EntityKind string
	EntityID   string

	// Free-form structured payload. Stored as JSON. Use for data the
	// UI may render later (e.g. blast_radius for an injection card).
	Payload map[string]any

	// Producer-supplied idempotency key. The same key within
	// DedupeWindow (default 10 min) is dropped silently.
	DedupeKey string

	// Explicit recipients. When empty, RecipientResolver figures it
	// out from EntityKind/EntityID + subscriptions. Producers that
	// already know exactly who should be notified populate this.
	Recipients []Recipient
}

// RecipientKind keeps the option open for non-user recipients (groups,
// roles, external webhooks) without breaking the publish signature.
type RecipientKind string

const (
	RecipientUser    RecipientKind = "user"
	RecipientRole    RecipientKind = "role"    // resolver expands to users
	RecipientService RecipientKind = "service" // for webhook fanout
)

// Recipient is a target the routing engine will route through channels.
// UserID is required for RecipientUser; RoleName for RecipientRole.
type Recipient struct {
	Kind     RecipientKind
	UserID   int
	RoleName string
}

// RenderedContent is what a channel actually delivers. Different
// channels can render the same Event very differently (markdown for
// inbox, HTML for email, Slack blocks for Slack). v1 only uses Title +
// Body.
type RenderedContent struct {
	Title string
	Body  string
	// Optional channel-specific blob (e.g. Slack block JSON, email
	// HTML). Channels interpret this however they want.
	Extra map[string]any
}

// PublishResult lets producers verify the event landed and inspect
// which recipients/channels it fanned out to. Mostly useful in tests
// and for tracing.
type PublishResult struct {
	NotificationIDs []int64
	Deliveries      []DeliveryAttemptSummary
	DroppedDedupe   bool
}

type DeliveryAttemptSummary struct {
	UserID  int
	Channel ChannelKey
	Status  DeliveryStatus
	Error   string
}

// Common errors. Channels return these to signal "not my fault, route
// elsewhere or skip silently".
var (
	ErrChannelNotConfigured = errors.New("channel not configured")
	ErrChannelDisabled      = errors.New("channel disabled by user preference")
	ErrEventDropped         = errors.New("event dropped by dedupe / cap")
)

// MarshalPayload is a small helper used in repository/inbox code where
// we need a stable JSON serialization of Event.Payload.
func MarshalPayload(p map[string]any) ([]byte, error) {
	if len(p) == 0 {
		return []byte("null"), nil
	}
	return json.Marshal(p)
}

// Publisher is the only interface producers depend on. Everything else
// in this package is an implementation detail of the orchestrator.
type Publisher interface {
	Publish(ctx context.Context, evt *Event) (*PublishResult, error)
}

// Clock is injectable so tests can pin time. Default implementation is
// realClock.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
