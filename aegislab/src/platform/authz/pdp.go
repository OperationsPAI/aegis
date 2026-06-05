package authz

import (
	"context"

	"aegis/platform/auth"
)

type Action string

type Resource struct {
	Type      string
	ID        int64
	ProjectID int64
	TaskID    string
}

type Decision struct {
	Allowed bool
	Reason  string
}

var (
	decisionAllow = &Decision{Allowed: true}
	decisionDeny  = &Decision{Allowed: false}
)

// PolicyTier is a single policy rule. Returning nil means "no opinion" and
// evaluation continues to the next tier. A non-nil return short-circuits.
type PolicyTier func(ctx context.Context, p auth.Principal, action Action, resource Resource) *Decision

type PDP struct {
	tiers    []PolicyTier
	resolver ProjectMembershipResolver
}

func NewPDP(resolver ProjectMembershipResolver, tiers ...PolicyTier) *PDP {
	return &PDP{tiers: tiers, resolver: resolver}
}

func (pdp *PDP) Decide(ctx context.Context, p auth.Principal, action Action, resource Resource) Decision {
	for _, tier := range pdp.tiers {
		if d := tier(ctx, p, action, resource); d != nil {
			return *d
		}
	}
	return Decision{Allowed: false, Reason: "default deny: no policy tier matched"}
}

func AdminTier() PolicyTier {
	return func(_ context.Context, p auth.Principal, _ Action, _ Resource) *Decision {
		if p.IsAdmin {
			return &Decision{Allowed: true, Reason: "admin"}
		}
		return nil
	}
}

// SystemScopeTier allows service-account callers that act on behalf of the
// platform itself (in-process orchestrator, gRPC intake). Replaces the
// pattern where callers used SystemScope().
func SystemScopeTier() PolicyTier {
	return func(_ context.Context, p auth.Principal, _ Action, _ Resource) *Decision {
		if p.Typ == auth.PrincipalServiceAccount {
			return &Decision{Allowed: true, Reason: "system scope (service account)"}
		}
		return nil
	}
}

func ServiceTaskOwnerTier() PolicyTier {
	return func(_ context.Context, p auth.Principal, _ Action, r Resource) *Decision {
		if p.Typ != auth.PrincipalTask && p.Typ != auth.PrincipalService {
			return nil
		}
		if p.TaskID == "" {
			return &Decision{Allowed: false, Reason: "service token missing task claim"}
		}
		if r.TaskID != "" && p.TaskID == r.TaskID {
			return &Decision{Allowed: true, Reason: "task owner"}
		}
		return &Decision{Allowed: false, Reason: "service token task mismatch"}
	}
}

// ProjectMemberTier checks whether the caller has visibility to the
// resource's owning project via the ProjectMembershipResolver.
func ProjectMemberTier(resolver ProjectMembershipResolver) PolicyTier {
	return func(ctx context.Context, p auth.Principal, _ Action, r Resource) *Decision {
		if p.Typ != auth.PrincipalHuman {
			return nil
		}
		if r.ProjectID == 0 {
			return nil
		}
		projects, err := resolver.VisibleProjects(ctx, int64(p.UserID))
		if err != nil {
			return &Decision{Allowed: false, Reason: "failed to resolve project membership: " + err.Error()}
		}
		for _, pid := range projects {
			if pid == r.ProjectID {
				return &Decision{Allowed: true, Reason: "project member"}
			}
		}
		return &Decision{Allowed: false, Reason: "project not visible"}
	}
}

// ScopeFromPrincipal constructs a CallerScope from a Principal, bridging the
// PDP into existing handler code that still uses CallerScope.
func ScopeFromPrincipal(ctx context.Context, p auth.Principal, resolver ProjectMembershipResolver) (CallerScope, error) {
	scope := CallerScope{
		UserID:  int64(p.UserID),
		IsAdmin: p.IsAdmin,
		TaskID:  p.TaskID,
	}
	switch p.Typ {
	case auth.PrincipalService, auth.PrincipalTask, auth.PrincipalServiceAccount:
		scope.IsService = true
	}
	if scope.IsService || scope.IsAdmin {
		return scope, nil
	}
	projects, err := resolver.VisibleProjects(ctx, scope.UserID)
	if err != nil {
		return CallerScope{}, err
	}
	scope.VisibleProjects = projects
	return scope, nil
}
