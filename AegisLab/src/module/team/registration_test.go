package team

import (
	"testing"

	"aegis/consts"
	"aegis/framework"
	"aegis/model"
)

// Compile-time interface checks.
var (
	_ Reader         = (*Service)(nil)
	_ HandlerService = (*Service)(nil)
)

func TestRoutesPortal(t *testing.T) {
	// RoutesPortal only populates struct fields; it never calls handler methods,
	// so a nil-service Handler is safe for metadata inspection.
	h := &Handler{service: nil}
	reg := RoutesPortal(h)

	if reg.Audience != framework.AudiencePortal {
		t.Errorf("Audience = %q, want %q", reg.Audience, framework.AudiencePortal)
	}
	if reg.Name != "team" {
		t.Errorf("Name = %q, want %q", reg.Name, "team")
	}
	if reg.Register == nil {
		t.Error("Register function must not be nil")
	}
}

func TestPermissions(t *testing.T) {
	reg := Permissions()

	if reg.Module != "team" {
		t.Errorf("Module = %q, want %q", reg.Module, "team")
	}
	if len(reg.Rules) == 0 {
		t.Fatal("Rules must not be empty")
	}

	expected := []consts.PermissionRule{
		consts.PermTeamReadAll,
		consts.PermTeamReadTeam,
		consts.PermTeamCreateAll,
		consts.PermTeamUpdateAll,
		consts.PermTeamUpdateTeam,
		consts.PermTeamDeleteAll,
		consts.PermTeamManageAll,
	}
	if len(reg.Rules) != len(expected) {
		t.Fatalf("len(Rules) = %d, want %d", len(reg.Rules), len(expected))
	}
	for i, rule := range expected {
		if reg.Rules[i] != rule {
			t.Errorf("Rules[%d] = %+v, want %+v", i, reg.Rules[i], rule)
		}
	}
}

func TestRoleGrants(t *testing.T) {
	reg := RoleGrants()

	if reg.Module != "team" {
		t.Errorf("Module = %q, want %q", reg.Module, "team")
	}
	if len(reg.Grants) == 0 {
		t.Fatal("Grants must not be empty")
	}

	// Admin role must have team permissions.
	adminGrants, ok := reg.Grants[consts.RoleAdmin]
	if !ok {
		t.Fatal("RoleAdmin must have grants")
	}
	if len(adminGrants) == 0 {
		t.Error("RoleAdmin grants must not be empty")
	}

	// Verify all expected roles are present.
	requiredRoles := []consts.RoleName{
		consts.RoleAdmin,
		consts.RoleTeamAdmin,
		consts.RoleTeamMember,
		consts.RoleTeamViewer,
	}
	for _, role := range requiredRoles {
		if _, exists := reg.Grants[role]; !exists {
			t.Errorf("missing grants for role %q", role)
		}
	}

	// RoleTeamAdmin should have the same permissions as RoleAdmin.
	teamAdminGrants := reg.Grants[consts.RoleTeamAdmin]
	if len(teamAdminGrants) != len(adminGrants) {
		t.Errorf("RoleTeamAdmin grants count = %d, want %d (same as RoleAdmin)",
			len(teamAdminGrants), len(adminGrants))
	}

	// RoleTeamViewer should have fewer permissions than RoleTeamMember.
	memberGrants := reg.Grants[consts.RoleTeamMember]
	viewerGrants := reg.Grants[consts.RoleTeamViewer]
	if len(viewerGrants) >= len(memberGrants) {
		t.Errorf("RoleTeamViewer grants (%d) should be fewer than RoleTeamMember (%d)",
			len(viewerGrants), len(memberGrants))
	}
}

func TestMigrations(t *testing.T) {
	reg := Migrations()

	if reg.Module != "team" {
		t.Errorf("Module = %q, want %q", reg.Module, "team")
	}
	if len(reg.Entities) == 0 {
		t.Fatal("Entities must not be empty")
	}

	// Verify &model.Team{} is among the entities.
	found := false
	for _, e := range reg.Entities {
		if _, ok := e.(*model.Team); ok {
			found = true
			break
		}
	}
	if !found {
		t.Error("Entities must contain *model.Team")
	}
}

func TestAsReader(t *testing.T) {
	// AsReader must accept *Service and return Reader.
	// We cannot construct a real Service without dependencies, but we can
	// verify the function signature compiles and returns the correct type.
	var r Reader = AsReader(&Service{})
	if r == nil {
		t.Error("AsReader must not return nil")
	}
}

func TestAsHandlerService(t *testing.T) {
	// Same approach: verify AsHandlerService compiles and returns non-nil.
	var hs HandlerService = AsHandlerService(&Service{})
	if hs == nil {
		t.Error("AsHandlerService must not return nil")
	}
}
