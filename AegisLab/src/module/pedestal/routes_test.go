package pedestal

import (
	"testing"

	"aegis/framework"
)

func TestRoutesAudience(t *testing.T) {
	h := NewHandler(nil) // nil service is fine; we only inspect registrar metadata
	reg := Routes(h)
	if reg.Audience != framework.AudiencePortal {
		t.Errorf("expected Audience=AudiencePortal, got %q", reg.Audience)
	}
	if reg.Name != "pedestal" {
		t.Errorf("expected Name=pedestal, got %q", reg.Name)
	}
	if reg.Register == nil {
		t.Error("expected non-nil Register func")
	}
}
