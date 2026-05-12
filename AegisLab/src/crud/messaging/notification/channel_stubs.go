package notification

import (
	"context"

	"github.com/sirupsen/logrus"
)

// The stubs below let the orchestrator + registry treat email / slack
// / webhook as first-class channels from day 1. Each is a no-op that
// returns ErrChannelNotConfigured — the publisher records `skipped`
// deliveries for them, which is the signal "wired in code, awaiting
// real adapter".
//
// When we ship a real email channel, replace the stub body with the
// SES/SMTP call; the rest of the system doesn't notice.

type EmailChannel struct{}

func NewEmailChannel() *EmailChannel { return &EmailChannel{} }
func (EmailChannel) Key() ChannelKey { return ChannelEmail }
func (EmailChannel) Deliver(_ context.Context, target *DeliveryTarget) error {
	logrus.WithFields(logrus.Fields{
		"channel":  "email",
		"user_id":  target.UserID,
		"category": target.Event.Category,
	}).Debug("email channel stub — not yet implemented")
	return ErrChannelNotConfigured
}

type SlackChannel struct{}

func NewSlackChannel() *SlackChannel { return &SlackChannel{} }
func (SlackChannel) Key() ChannelKey { return ChannelSlack }
func (SlackChannel) Deliver(_ context.Context, target *DeliveryTarget) error {
	logrus.WithFields(logrus.Fields{
		"channel":  "slack",
		"user_id":  target.UserID,
		"category": target.Event.Category,
	}).Debug("slack channel stub — not yet implemented")
	return ErrChannelNotConfigured
}

type WebhookChannel struct{}

func NewWebhookChannel() *WebhookChannel { return &WebhookChannel{} }
func (WebhookChannel) Key() ChannelKey { return ChannelWebhook }
func (WebhookChannel) Deliver(_ context.Context, target *DeliveryTarget) error {
	logrus.WithFields(logrus.Fields{
		"channel":  "webhook",
		"user_id":  target.UserID,
		"category": target.Event.Category,
	}).Debug("webhook channel stub — not yet implemented")
	return ErrChannelNotConfigured
}
