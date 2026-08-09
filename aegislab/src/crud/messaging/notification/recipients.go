package notification

import (
	"context"
	"fmt"
)

// RecipientResolver expands an Event into the concrete users that
// should hear about it. Producers can supply explicit Recipients on
// the Event for "we already know exactly who" cases (this is the v1
// path); the resolver picks up the rest — role fanout, entity
// watchers — in later phases.
//
// Keeping this as an interface from day 1 means producers never
// switch on "did I specify recipients or not" — they just publish,
// the orchestrator routes.
type RecipientResolver interface {
	Resolve(ctx context.Context, evt *Event) ([]int, error)
}

// RoleExpander resolves a role name to the user ids that hold it. The
// rbac module supplies a real implementation; the publisher passes it
// in via fx. Held behind an interface so module/notification doesn't
// import module/rbac directly (avoids the import cycle and keeps the
// notification package independently testable).
type RoleExpander interface {
	UsersWithRole(ctx context.Context, role string) ([]int, error)
}

// NopRoleExpander returns no users. Wired by default; consumers swap
// in an rbac-backed one when needed.
type NopRoleExpander struct{}

func (NopRoleExpander) UsersWithRole(_ context.Context, _ string) ([]int, error) {
	return nil, nil
}

// EntityWatcherResolver answers "who is watching this entity right
// now". An empty default lets v1 ship without an entity-watchers
// table; modules like dataset / injection can register their own
// implementation via fx (e.g. project membership lookup).
type EntityWatcherResolver interface {
	Watchers(ctx context.Context, kind, id string) ([]int, error)
}

type NopEntityWatcherResolver struct{}

func (NopEntityWatcherResolver) Watchers(_ context.Context, _, _ string) ([]int, error) {
	return nil, nil
}

// DefaultRecipientResolver composes the three resolution paths in a
// deterministic order — explicit users > role fanout > entity
// watchers — and de-dupes. Producers only need to think about the
// path that's relevant to them.
type DefaultRecipientResolver struct {
	Roles    RoleExpander
	Watchers EntityWatcherResolver
}

func NewDefaultRecipientResolver(roles RoleExpander, watchers EntityWatcherResolver) *DefaultRecipientResolver {
	if roles == nil {
		roles = NopRoleExpander{}
	}
	if watchers == nil {
		watchers = NopEntityWatcherResolver{}
	}
	return &DefaultRecipientResolver{Roles: roles, Watchers: watchers}
}

func (r *DefaultRecipientResolver) Resolve(ctx context.Context, evt *Event) ([]int, error) {
	if evt == nil {
		return nil, fmt.Errorf("nil event")
	}
	seen := make(map[int]struct{}, len(evt.Recipients))
	out := make([]int, 0, len(evt.Recipients))

	add := func(id int) {
		if id <= 0 {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	for _, rcpt := range evt.Recipients {
		switch rcpt.Kind {
		case RecipientUser, "":
			add(rcpt.UserID)
		case RecipientRole:
			users, err := r.Roles.UsersWithRole(ctx, rcpt.RoleName)
			if err != nil {
				return nil, fmt.Errorf("resolve role %q: %w", rcpt.RoleName, err)
			}
			for _, u := range users {
				add(u)
			}
		case RecipientService:
			// service-targeted deliveries are handled by the
			// (future) webhook channel; they have no inbox row.
			continue
		}
	}

	if evt.EntityKind != "" && evt.EntityID != "" {
		watchers, err := r.Watchers.Watchers(ctx, evt.EntityKind, evt.EntityID)
		if err != nil {
			return nil, fmt.Errorf("resolve watchers: %w", err)
		}
		for _, u := range watchers {
			add(u)
		}
	}

	return out, nil
}
