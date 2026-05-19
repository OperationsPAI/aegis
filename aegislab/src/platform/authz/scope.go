package authz

import (
	"context"
	"errors"
	"fmt"

	"aegis/platform/consts"

	"github.com/gin-gonic/gin"
)

type CallerScope struct {
	UserID          int64
	IsAdmin         bool
	IsService       bool
	TaskID          string
	VisibleProjects []int64
}

// SystemScope is for in-process trusted callers (orchestrator, gRPC intake).
// External handlers MUST go through ScopeFromGinContext.
func SystemScope() CallerScope { return CallerScope{IsAdmin: true} }

var (
	ErrProjectOutsideScope = errors.New("project outside caller scope")
	ErrMissingAuth         = errors.New("authz: missing authenticated user on request")
	ErrServiceTaskMismatch = errors.New("authz: service token task mismatch")
)

func (s CallerScope) AllowsAllProjects() bool { return s.IsAdmin }

// MustOwnTask asserts that a service-token caller may only touch resources
// owned by the TaskID baked into its claim. Admin and non-service callers
// are no-ops (their authority is established elsewhere — project scope,
// permission rules, ownership checks). Service tokens with an empty claim
// TaskID are rejected outright: they predate the claim and should not be
// used to mutate per-task resources.
func (s CallerScope) MustOwnTask(taskID string) error {
	if !s.IsService || s.IsAdmin {
		return nil
	}
	if s.TaskID == "" || s.TaskID != taskID {
		return fmt.Errorf("%w: claim=%q resource=%q", ErrServiceTaskMismatch, s.TaskID, taskID)
	}
	return nil
}

func (s CallerScope) MustHaveProject(projectID int64) error {
	if s.IsAdmin {
		return nil
	}
	// Service tokens are gated per-resource via MustOwnTask. They authenticate
	// against a TaskID claim, not a user — so they don't have VisibleProjects.
	// Reads via service tokens (e.g. BuildDatapack pod fetching its parent
	// injection) trust the orchestrator to only issue tokens scoped to the
	// task's own trace lineage.
	if s.IsService {
		return nil
	}
	for _, p := range s.VisibleProjects {
		if p == projectID {
			return nil
		}
	}
	return fmt.Errorf("%w: project_id=%d", ErrProjectOutsideScope, projectID)
}

// ProjectMembershipResolver loads visible projects for a user. Backed by
// user_scoped_roles where scope_type="aegis.project" AND status=enabled.
type ProjectMembershipResolver interface {
	VisibleProjects(ctx context.Context, userID int64) ([]int64, error)
}

const cachedScopeKey = "authz.caller_scope"

// ScopeFromGinContext reads the per-request CallerScope using context keys set
// by platform/middleware/auth.go and resolves project membership via resolver.
// Result is cached on the *gin.Context for the duration of the request.
func ScopeFromGinContext(c *gin.Context, resolver ProjectMembershipResolver) (CallerScope, error) {
	if cached, ok := c.Get(cachedScopeKey); ok {
		if s, ok := cached.(CallerScope); ok {
			return s, nil
		}
	}

	scope := CallerScope{}

	if v, ok := c.Get(consts.CtxKeyIsAdmin); ok {
		if b, ok := v.(bool); ok {
			scope.IsAdmin = b
		}
	}
	if v, ok := c.Get(consts.CtxKeyIsServiceToken); ok {
		if b, ok := v.(bool); ok {
			scope.IsService = b
		}
	}
	if v, ok := c.Get(consts.CtxKeyTaskID); ok {
		if s, ok := v.(string); ok {
			scope.TaskID = s
		}
	}

	// Service tokens authenticate via TaskID claim; they don't carry a UserID.
	// The per-method MustOwnTask check is responsible for the actual gate.
	// VisibleProjects stays nil for service tokens; service-token callers MUST
	// pass through admin or MustOwnTask paths, never MustHaveProject alone.
	if scope.IsService {
		c.Set(cachedScopeKey, scope)
		return scope, nil
	}

	uidRaw, ok := c.Get(consts.CtxKeyUserID)
	if !ok {
		return CallerScope{}, ErrMissingAuth
	}
	uid, err := toInt64(uidRaw)
	if err != nil {
		return CallerScope{}, fmt.Errorf("authz: bad user_id type: %w", err)
	}
	scope.UserID = uid

	if !scope.IsAdmin {
		projects, err := resolver.VisibleProjects(c.Request.Context(), uid)
		if err != nil {
			return CallerScope{}, fmt.Errorf("authz: resolve visible projects: %w", err)
		}
		scope.VisibleProjects = projects
	}

	c.Set(cachedScopeKey, scope)
	return scope, nil
}

func toInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int64:
		return x, nil
	case uint:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case uint64:
		return int64(x), nil
	default:
		return 0, fmt.Errorf("unsupported numeric type %T", v)
	}
}
