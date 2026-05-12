package pedestal

import (
	"testing"

	"aegis/platform/model"
)

func TestMigrationsModule(t *testing.T) {
	reg := Migrations()
	if reg.Module != "pedestal" {
		t.Errorf("expected Module=pedestal, got %q", reg.Module)
	}
	if len(reg.Entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(reg.Entities))
	}
	if _, ok := reg.Entities[0].(*model.HelmConfig); !ok {
		t.Errorf("expected *model.HelmConfig, got %T", reg.Entities[0])
	}
}
