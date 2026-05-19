package authz

import (
	"errors"
	"testing"
)

func TestSystemScopeAllowsAllProjects(t *testing.T) {
	s := SystemScope()
	if !s.AllowsAllProjects() {
		t.Fatal("SystemScope must allow all projects")
	}
	if err := s.MustHaveProject(42); err != nil {
		t.Fatalf("SystemScope.MustHaveProject: %v", err)
	}
}

func TestMustHaveProjectOutsideScope(t *testing.T) {
	s := CallerScope{UserID: 1, VisibleProjects: []int64{7}}
	if err := s.MustHaveProject(9); !errors.Is(err, ErrProjectOutsideScope) {
		t.Fatalf("want ErrProjectOutsideScope, got %v", err)
	}
	if err := s.MustHaveProject(7); err != nil {
		t.Fatalf("project 7 should be visible: %v", err)
	}
}
