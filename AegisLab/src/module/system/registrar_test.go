package system

import (
	"testing"

	"aegis/consts"
)

func TestPermissionsRegistrarModule(t *testing.T) {
	p := Permissions()
	if p.Module != "system" {
		t.Fatalf("expected module 'system', got %q", p.Module)
	}
	if len(p.Rules) != 8 {
		t.Fatalf("expected 8 permission rules, got %d", len(p.Rules))
	}
}

func TestRoleGrantsRegistrarGrantsAdmin(t *testing.T) {
	rg := RoleGrants()
	if rg.Module != "system" {
		t.Fatalf("expected module 'system', got %q", rg.Module)
	}
	grants, ok := rg.Grants[consts.RoleAdmin]
	if !ok {
		t.Fatal("expected RoleAdmin grants to be present")
	}
	if len(grants) != 8 {
		t.Fatalf("expected 8 RoleAdmin grants, got %d", len(grants))
	}
}

func TestMigrationsRegistrarEntities(t *testing.T) {
	m := Migrations()
	if m.Module != "system" {
		t.Fatalf("expected module 'system', got %q", m.Module)
	}
	if len(m.Entities) != 6 {
		t.Fatalf("expected 6 migration entities, got %d", len(m.Entities))
	}
}

func TestRepositoryImplementsReader(t *testing.T) {
	var _ Reader = (*Repository)(nil)
}
