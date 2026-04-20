package widget

import (
	"testing"

	"aegis/consts"
	"aegis/framework"
	"aegis/model"
)

func TestRoutes(t *testing.T) {
	r := Routes(nil)
	if r.Name != "widget" {
		t.Fatalf("expected widget route registrar, got %q", r.Name)
	}
	if r.Audience != framework.AudienceAdmin {
		t.Fatalf("expected admin audience, got %q", r.Audience)
	}
	if r.Register == nil {
		t.Fatal("expected register func")
	}
}

func TestPermissions(t *testing.T) {
	reg := Permissions()
	if len(reg.Rules) != 1 || reg.Rules[0] != PermWidgetReadAll {
		t.Fatalf("unexpected widget permissions: %+v", reg.Rules)
	}
	grants := RoleGrants().Grants
	if len(grants[consts.RoleAdmin]) != 1 || grants[consts.RoleAdmin][0] != PermWidgetReadAll {
		t.Fatalf("admin grants missing widget read: %+v", grants[consts.RoleAdmin])
	}
}

func TestMigrations(t *testing.T) {
	reg := Migrations()
	if len(reg.Entities) != 1 {
		t.Fatalf("expected 1 migration entity, got %d", len(reg.Entities))
	}
	if _, ok := reg.Entities[0].(*model.Widget); !ok {
		t.Fatalf("expected widget entity, got %T", reg.Entities[0])
	}
}
