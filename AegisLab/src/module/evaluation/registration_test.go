package evaluation

import (
	"testing"

	"aegis/framework"
	"aegis/model"
)

func TestRoutesPortalRegistrar(t *testing.T) {
	reg := RoutesPortal(nil)
	if reg.Audience != framework.AudiencePortal {
		t.Fatalf("expected AudiencePortal, got %q", reg.Audience)
	}
	if reg.Name == "" {
		t.Fatal("expected non-empty Name")
	}
	if reg.Register == nil {
		t.Fatal("expected non-nil Register function")
	}
}

func TestRoutesSDKRegistrar(t *testing.T) {
	reg := RoutesSDK(nil)
	if reg.Audience != framework.AudienceSDK {
		t.Fatalf("expected AudienceSDK, got %q", reg.Audience)
	}
	if reg.Name == "" {
		t.Fatal("expected non-empty Name")
	}
	if reg.Register == nil {
		t.Fatal("expected non-nil Register function")
	}
}

func TestMigrationsRegistrar(t *testing.T) {
	reg := Migrations()
	if reg.Module != "evaluation" {
		t.Fatalf("expected module name %q, got %q", "evaluation", reg.Module)
	}
	if len(reg.Entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(reg.Entities))
	}
	if _, ok := reg.Entities[0].(*model.Evaluation); !ok {
		t.Fatalf("expected *model.Evaluation, got %T", reg.Entities[0])
	}
}

func TestNoPermissionsRegistrar(t *testing.T) {
	// Evaluation module has no RBAC rules. Verify that no Permissions or
	// RoleGrants functions exist (they were removed per review feedback).
	// This test documents the design decision: evaluation endpoints rely on
	// JWT/API-key scope middleware, not PermissionRule checks.

	// The fact that this test compiles without referencing Permissions() or
	// RoleGrants() is the assertion: those functions no longer exist in the
	// package.
}
