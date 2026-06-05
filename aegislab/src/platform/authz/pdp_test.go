package authz

import (
	"context"
	"errors"
	"testing"

	"aegis/platform/auth"
)

func TestPDP_AdminAllowed(t *testing.T) {
	pdp := NewPDP(stubResolver{},
		AdminTier(),
		ProjectMemberTier(stubResolver{}),
	)
	d := pdp.Decide(context.Background(), auth.Principal{IsAdmin: true, Typ: auth.PrincipalHuman}, "delete", Resource{Type: "project", ID: 1})
	if !d.Allowed {
		t.Fatalf("admin should be allowed, got: %+v", d)
	}
}

func TestPDP_ServiceTaskOwner(t *testing.T) {
	pdp := NewPDP(stubResolver{},
		ServiceTaskOwnerTier(),
	)
	p := auth.Principal{Typ: auth.PrincipalTask, TaskID: "task-42"}
	d := pdp.Decide(context.Background(), p, "read", Resource{Type: "injection", TaskID: "task-42"})
	if !d.Allowed {
		t.Fatalf("task owner should be allowed, got: %+v", d)
	}
}

func TestPDP_ServiceTaskMismatch(t *testing.T) {
	pdp := NewPDP(stubResolver{},
		ServiceTaskOwnerTier(),
	)
	p := auth.Principal{Typ: auth.PrincipalTask, TaskID: "task-42"}
	d := pdp.Decide(context.Background(), p, "read", Resource{Type: "injection", TaskID: "task-99"})
	if d.Allowed {
		t.Fatalf("task mismatch should be denied, got: %+v", d)
	}
}

func TestPDP_ProjectMember(t *testing.T) {
	pdp := NewPDP(stubResolver{projects: []int64{10, 20}},
		AdminTier(),
		ServiceTaskOwnerTier(),
		ProjectMemberTier(stubResolver{projects: []int64{10, 20}}),
	)
	p := auth.Principal{Typ: auth.PrincipalHuman, UserID: 5}
	d := pdp.Decide(context.Background(), p, "read", Resource{Type: "injection", ProjectID: 10})
	if !d.Allowed {
		t.Fatalf("project member should be allowed, got: %+v", d)
	}
}

func TestPDP_ProjectNotVisible(t *testing.T) {
	pdp := NewPDP(stubResolver{projects: []int64{10}},
		AdminTier(),
		ServiceTaskOwnerTier(),
		ProjectMemberTier(stubResolver{projects: []int64{10}}),
	)
	p := auth.Principal{Typ: auth.PrincipalHuman, UserID: 5}
	d := pdp.Decide(context.Background(), p, "read", Resource{Type: "injection", ProjectID: 99})
	if d.Allowed {
		t.Fatalf("non-member should be denied, got: %+v", d)
	}
}

func TestPDP_TierOrder(t *testing.T) {
	// Admin tier fires first, so even though the project tier would deny,
	// admin wins.
	pdp := NewPDP(stubResolver{},
		AdminTier(),
		ProjectMemberTier(stubResolver{projects: nil}),
	)
	p := auth.Principal{Typ: auth.PrincipalHuman, IsAdmin: true, UserID: 1}
	d := pdp.Decide(context.Background(), p, "delete", Resource{Type: "project", ProjectID: 99})
	if !d.Allowed || d.Reason != "admin" {
		t.Fatalf("admin tier should short-circuit, got: %+v", d)
	}
}

func TestPDP_DefaultDeny(t *testing.T) {
	pdp := NewPDP(stubResolver{})
	p := auth.Principal{Typ: auth.PrincipalHuman, UserID: 1}
	d := pdp.Decide(context.Background(), p, "read", Resource{Type: "project", ID: 1})
	if d.Allowed {
		t.Fatalf("empty tier list should default deny, got: %+v", d)
	}
}

func TestScopeFromPrincipal_Human(t *testing.T) {
	p := auth.Principal{
		Typ:    auth.PrincipalHuman,
		UserID: 7,
	}
	scope, err := ScopeFromPrincipal(context.Background(), p, stubResolver{projects: []int64{3, 5}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scope.UserID != 7 {
		t.Fatalf("UserID=%d, want 7", scope.UserID)
	}
	if scope.IsService || scope.IsAdmin {
		t.Fatalf("human principal should not be service/admin: %+v", scope)
	}
	if len(scope.VisibleProjects) != 2 || scope.VisibleProjects[0] != 3 {
		t.Fatalf("VisibleProjects=%v, want [3 5]", scope.VisibleProjects)
	}
}

func TestScopeFromPrincipal_Service(t *testing.T) {
	p := auth.Principal{
		Typ:    auth.PrincipalService,
		TaskID: "task-X",
	}
	scope, err := ScopeFromPrincipal(context.Background(), p, stubResolver{err: errors.New("must not be called")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !scope.IsService {
		t.Fatal("service principal should set IsService")
	}
	if scope.TaskID != "task-X" {
		t.Fatalf("TaskID=%q, want task-X", scope.TaskID)
	}
	if scope.VisibleProjects != nil {
		t.Fatalf("service should not have VisibleProjects: %v", scope.VisibleProjects)
	}
}
