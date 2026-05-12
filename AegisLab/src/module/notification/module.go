package notification

import (
	redisinfra "aegis/infra/redis"

	"go.uber.org/fx"
	"gorm.io/gorm"
)

// Module wires the notification skeleton: 6 roles + the legacy stream
// handler. Two consumers depend on this:
//
//   - the monolith (aegis-backend) — every role is in-process,
//     producers depend on Publisher directly via fx.
//   - the standalone microservice (`app/notify`) — same wiring, plus
//     an HTTP ingestion handler that turns inbound
//     `/v1/events:publish` requests into Publisher.Publish calls.
//
// Adding a new channel = drop a new Channel provider into the
// channels group + (optionally) enable it in the default
// PreferenceEngine. Producers never change.
var Module = fx.Module("notification",
	// 6-role skeleton: repos + interface adapters
	fx.Provide(NewInboxRepository),
	fx.Provide(NewSubscriptionRepository),
	fx.Provide(NewDBDeliveryLogger, asDeliveryLogger),
	fx.Provide(NewPassthroughTemplater, asTemplater),
	fx.Provide(NewNopRoleExpander, asRoleExpander),
	fx.Provide(NewNopEntityWatcherResolver, asEntityWatcherResolver),
	fx.Provide(NewDefaultRecipientResolver, asRecipientResolver),
	fx.Provide(NewPreferenceEngineWithInboxOnly, asPreferenceEngine),

	// Channels — each registers into a fx group so the registry
	// picks them up without the publisher knowing concretes.
	fx.Provide(
		fx.Annotate(NewInboxChannel, fx.ResultTags(`group:"notification.channels"`), fx.As(new(Channel))),
		fx.Annotate(NewEmailChannel, fx.ResultTags(`group:"notification.channels"`), fx.As(new(Channel))),
		fx.Annotate(NewSlackChannel, fx.ResultTags(`group:"notification.channels"`), fx.As(new(Channel))),
		fx.Annotate(NewWebhookChannel, fx.ResultTags(`group:"notification.channels"`), fx.As(new(Channel))),
	),
	fx.Provide(fx.Annotate(newChannelRegistry, fx.ParamTags(`group:"notification.channels"`))),

	fx.Provide(NewClock),
	fx.Provide(newLocalPublisher, asPublisher),

	// PubSubClient — narrow surface so a future remote-pubsub impl
	// can swap in without touching InboxHandler.
	fx.Provide(asPubSubClient),

	// HTTP surface
	fx.Provide(NewInboxHandler),
	fx.Provide(
		fx.Annotate(RoutesPortalInbox, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(RoutesSDKIngestion, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)

// Interface adapters. Trivial but needed so fx can resolve interface
// dependencies without an explicit fx.As on every concrete.

func asDeliveryLogger(l *DBDeliveryLogger) DeliveryLogger                      { return l }
func asTemplater(t *PassthroughTemplater) Templater                            { return t }
func asRoleExpander(r NopRoleExpander) RoleExpander                            { return r }
func asEntityWatcherResolver(r NopEntityWatcherResolver) EntityWatcherResolver { return r }
func asRecipientResolver(r *DefaultRecipientResolver) RecipientResolver        { return r }
func asPreferenceEngine(e *DefaultPreferenceEngine) PreferenceEngine           { return e }
func asPublisher(p *LocalPublisher) Publisher                                  { return p }

// NewClock returns the real wall-clock implementation. Tests override
// by providing a stub Clock in fx.Decorate.
func NewClock() Clock { return realClock{} }

// NewNopRoleExpander / NewNopEntityWatcherResolver — public
// constructors so fx can wire the defaults. Real implementations
// land in module/rbac and entity modules (dataset, injection, …)
// once watcher/role-fanout becomes a real feature.
func NewNopRoleExpander() NopRoleExpander                   { return NopRoleExpander{} }
func NewNopEntityWatcherResolver() NopEntityWatcherResolver { return NopEntityWatcherResolver{} }

// NewPreferenceEngineWithInboxOnly is the v1 default — only the
// inbox channel is considered. Replace this provider (via
// fx.Decorate or a fx.Replace) when an external channel goes live.
func NewPreferenceEngineWithInboxOnly(db *gorm.DB) *DefaultPreferenceEngine {
	return NewDefaultPreferenceEngine(db, []ChannelKey{ChannelInbox})
}

func newChannelRegistry(channels []Channel) *Registry { return NewRegistry(channels) }

func newLocalPublisher(
	inbox *InboxRepository,
	resolver RecipientResolver,
	prefs PreferenceEngine,
	tmpl Templater,
	registry *Registry,
	logger DeliveryLogger,
	clock Clock,
) *LocalPublisher {
	return NewLocalPublisher(LocalPublisherDeps{
		Inbox:    inbox,
		Resolver: resolver,
		Prefs:    prefs,
		Tmpl:     tmpl,
		Channels: registry,
		Delivery: logger,
		Clock:    clock,
	})
}

// asPubSubClient adapts the existing infra Gateway to the narrow
// PubSubClient surface the inbox handler needs. A future remote-mode
// implementation (Kafka / NATS) replaces this provider only.
func asPubSubClient(g *redisinfra.Gateway) PubSubClient { return g }
