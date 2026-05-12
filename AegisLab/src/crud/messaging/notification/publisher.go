package notification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// LocalPublisher is the in-process Publisher implementation. It owns
// the orchestration across the six roles:
//
//   1. ingestion        — validate the inbound Event
//   2. recipients       — resolve users via RecipientResolver
//   3. preferences      — for each user, pick channels via PreferenceEngine
//   4. templating       — render content per channel via Templater
//   5. delivery         — call each Channel.Deliver
//   6. observability    — record every attempt via DeliveryLogger
//
// All six are injected interfaces — swap any of them without touching
// producers. The same LocalPublisher is used both inside the
// monolith (producers depend on Publisher via fx) and inside the
// standalone notification microservice (its HTTP ingestion handler
// also calls Publisher.Publish).
type LocalPublisher struct {
	inbox    *InboxRepository
	resolver RecipientResolver
	prefs    PreferenceEngine
	tmpl     Templater
	channels *Registry
	delivery DeliveryLogger
	clock    Clock
}

// LocalPublisherDeps is the constructor input. Keeping every wire
// explicit makes it trivial to substitute test doubles.
type LocalPublisherDeps struct {
	Inbox    *InboxRepository
	Resolver RecipientResolver
	Prefs    PreferenceEngine
	Tmpl     Templater
	Channels *Registry
	Delivery DeliveryLogger
	Clock    Clock
}

func NewLocalPublisher(d LocalPublisherDeps) *LocalPublisher {
	if d.Clock == nil {
		d.Clock = realClock{}
	}
	if d.Delivery == nil {
		d.Delivery = NopDeliveryLogger{}
	}
	return &LocalPublisher{
		inbox:    d.Inbox,
		resolver: d.Resolver,
		prefs:    d.Prefs,
		tmpl:     d.Tmpl,
		channels: d.Channels,
		delivery: d.Delivery,
		clock:    d.Clock,
	}
}

// Publish runs the full pipeline. Errors from the orchestration layer
// (validation, recipient resolution) abort. Errors from individual
// channel deliveries are logged + recorded — they never abort the
// publish, because partial fanout is still better than nothing.
func (p *LocalPublisher) Publish(ctx context.Context, evt *Event) (*PublishResult, error) {
	if err := validateEvent(evt); err != nil {
		return nil, err
	}
	if evt.Severity == "" {
		evt.Severity = SeverityInfo
	}

	users, err := p.resolver.Resolve(ctx, evt)
	if err != nil {
		return nil, fmt.Errorf("resolve recipients: %w", err)
	}
	if len(users) == 0 {
		return &PublishResult{}, nil
	}

	result := &PublishResult{}
	for _, userID := range users {
		notif, dropped, err := p.persistInbox(ctx, userID, evt)
		if err != nil {
			return nil, fmt.Errorf("persist inbox for user %d: %w", userID, err)
		}
		if dropped {
			result.DroppedDedupe = true
			continue
		}
		result.NotificationIDs = append(result.NotificationIDs, notif.ID)

		channels, err := p.prefs.Route(ctx, userID, evt)
		if err != nil {
			logrus.WithError(err).WithField("user_id", userID).
				Warn("preference routing failed; skipping user")
			continue
		}
		for _, ch := range channels {
			summary := p.dispatch(ctx, userID, notif.ID, ch, evt)
			result.Deliveries = append(result.Deliveries, summary)
		}
	}
	return result, nil
}

func validateEvent(evt *Event) error {
	if evt == nil {
		return errors.New("nil event")
	}
	if strings.TrimSpace(evt.Category) == "" {
		return errors.New("event.Category is required")
	}
	if strings.TrimSpace(evt.Title) == "" {
		return errors.New("event.Title is required")
	}
	return nil
}

// persistInbox is responsible for the dedupe + Notification row create
// in one place. Returning the row lets the caller attach deliveries.
func (p *LocalPublisher) persistInbox(ctx context.Context, userID int, evt *Event) (*Notification, bool, error) {
	if evt.DedupeKey != "" {
		since := p.clock.Now().Add(-dedupeWindow)
		existing, err := p.inbox.FindByDedupeKey(ctx, userID, evt.DedupeKey, since)
		if err != nil {
			return nil, false, err
		}
		if existing != nil {
			return existing, true, nil
		}
	}

	payload, err := MarshalPayload(evt.Payload)
	if err != nil {
		return nil, false, fmt.Errorf("marshal payload: %w", err)
	}
	n := &Notification{
		UserID:      userID,
		Category:    evt.Category,
		Severity:    string(evt.Severity),
		Title:       evt.Title,
		Body:        evt.Body,
		LinkTo:      evt.LinkTo,
		ActorUserID: evt.ActorUserID,
		EntityKind:  evt.EntityKind,
		EntityID:    evt.EntityID,
		Payload:     payload,
		DedupeKey:   evt.DedupeKey,
	}
	if err := p.inbox.Create(ctx, n); err != nil {
		return nil, false, err
	}
	return n, false, nil
}

func (p *LocalPublisher) dispatch(ctx context.Context, userID int, notifID int64, ch ChannelKey, evt *Event) DeliveryAttemptSummary {
	summary := DeliveryAttemptSummary{UserID: userID, Channel: ch, Status: DeliveryQueued}

	channel, err := p.channels.Get(ch)
	if err != nil {
		summary.Status = DeliverySkipped
		summary.Error = err.Error()
		p.logDelivery(ctx, notifID, userID, ch, DeliverySkipped, err)
		return summary
	}

	content, err := p.tmpl.Render(ctx, ch, evt)
	if err != nil {
		summary.Status = DeliveryFailed
		summary.Error = err.Error()
		p.logDelivery(ctx, notifID, userID, ch, DeliveryFailed, err)
		return summary
	}

	target := &DeliveryTarget{
		UserID:         userID,
		NotificationID: notifID,
		Content:        content,
		Event:          evt,
	}
	if err := channel.Deliver(ctx, target); err != nil {
		if errors.Is(err, ErrChannelNotConfigured) || errors.Is(err, ErrChannelDisabled) {
			summary.Status = DeliverySkipped
		} else {
			summary.Status = DeliveryFailed
		}
		summary.Error = err.Error()
		p.logDelivery(ctx, notifID, userID, ch, summary.Status, err)
		return summary
	}
	summary.Status = DeliveryDelivered
	p.logDelivery(ctx, notifID, userID, ch, DeliveryDelivered, nil)
	return summary
}

func (p *LocalPublisher) logDelivery(ctx context.Context, notifID int64, userID int, ch ChannelKey, status DeliveryStatus, err error) {
	attempt := DeliveryAttempt{
		NotificationID: notifID,
		UserID:         userID,
		Channel:        ch,
		Status:         status,
	}
	if err != nil {
		attempt.Error = err.Error()
	}
	if logErr := p.delivery.Record(ctx, attempt); logErr != nil {
		logrus.WithError(logErr).Warn("failed to record delivery attempt")
	}
}

// Static interface check.
var _ Publisher = (*LocalPublisher)(nil)

// IsContextDone helper avoids the dep cycle when the orchestrator
// wants to surface a richer error than ctx.Err().
func IsContextDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// ensureTime helper used by the dedupe window; small but lets us pin
// time in tests without touching the publisher signature.
func ensureTime(c Clock) time.Time {
	if c == nil {
		return time.Now()
	}
	return c.Now()
}

var (
	_ = gorm.ErrRecordNotFound // imported via repository indirectly; kept here for build-stable imports if delivery surface changes
)
