package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
)

// CallerScope describes the authorization boundary for a request. It is the
// single source of truth for "which projects may this caller act on?" and is
// passed explicitly through domain services rather than being re-derived from
// request context downstream.
type CallerScope struct {
	UserID          int64
	IsAdmin         bool
	IsService       bool
	TaskID          string
	VisibleProjects []int64
}

// SystemScope returns a scope for in-process trusted callers (orchestrator,
// gRPC intake). External HTTP handlers MUST NOT use this; they go through
// ScopeFromGinContext so the per-user membership is honored.
func SystemScope() CallerScope { return CallerScope{IsAdmin: true} }

// ErrProjectOutsideScope is returned when the caller attempts to act on a
// project that is not in their visible set (and they are not admin).
var ErrProjectOutsideScope = errors.New("project outside caller scope")

// AllowsAllProjects reports whether the scope grants visibility to every
// project (admin or system).
func (s CallerScope) AllowsAllProjects() bool { return s.IsAdmin }

// MustHaveProject returns nil iff the scope permits acting on projectID.
func (s CallerScope) MustHaveProject(projectID int64) error {
	if s.IsAdmin {
		return nil
	}
	for _, p := range s.VisibleProjects {
		if p == projectID {
			return nil
		}
	}
	return fmt.Errorf("%w: project_id=%d", ErrProjectOutsideScope, projectID)
}

// ProjectMembershipResolver loads the set of projects a user can see. Backed
// by user_scope_assignments where scope_type="project" AND status=enabled.
type ProjectMembershipResolver interface {
	VisibleProjects(ctx context.Context, userID int64) ([]int64, error)
}

// ScopeFromGinContext is the skeleton entry-point for HTTP handlers. Wiring
// to the existing middleware context keys is filled in by a later commit.
func ScopeFromGinContext(_ *gin.Context, _ ProjectMembershipResolver) (CallerScope, error) {
	return CallerScope{}, errors.New("authz.ScopeFromGinContext: not yet wired")
}
