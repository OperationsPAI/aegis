package pedestal

import "testing"

func TestPermissionsModule(t *testing.T) {
	reg := Permissions()
	if reg.Module != "pedestal" {
		t.Errorf("expected Module=pedestal, got %q", reg.Module)
	}
	if len(reg.Rules) != 0 {
		t.Errorf("expected empty Rules, got %d entries", len(reg.Rules))
	}
}

func TestRoleGrantsModule(t *testing.T) {
	reg := RoleGrants()
	if reg.Module != "pedestal" {
		t.Errorf("expected Module=pedestal, got %q", reg.Module)
	}
	if len(reg.Grants) != 0 {
		t.Errorf("expected empty Grants, got %d entries", len(reg.Grants))
	}
}
