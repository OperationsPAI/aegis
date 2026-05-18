package chaossystem

import (
	"testing"

	"aegis/platform/framework"

	"github.com/gin-gonic/gin"
)

func TestRoutesPortalReturnsPortalAudience(t *testing.T) {
	handler := &Handler{}
	reg := RoutesPortal(handler)

	if reg.Audience != framework.AudiencePortal {
		t.Fatalf("expected AudiencePortal, got %q", reg.Audience)
	}
	if reg.Name != "chaossystem.portal" {
		t.Fatalf("expected name %q, got %q", "chaossystem.portal", reg.Name)
	}
	if reg.Register == nil {
		t.Fatal("expected Register to be non-nil")
	}
}

func TestRoutesPortalRegistersReadOnlyEndpoints(t *testing.T) {
	handler := &Handler{}
	reg := RoutesPortal(handler)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v2 := engine.Group("/api/v2")
	reg.Register(v2)

	type key struct {
		method string
		path   string
	}
	expected := map[key]bool{
		{"GET", "/api/v2/systems"}:                                 false,
		{"GET", "/api/v2/systems/:id"}:                             false,
		{"GET", "/api/v2/systems/by-name/:name/inject-candidates"}: false,
	}

	// Routes that must NOT be exposed under Portal audience — these are
	// governance / admin-only mutations.
	forbidden := map[key]struct{}{
		{"POST", "/api/v2/systems"}:                                      {},
		{"PUT", "/api/v2/systems/:id"}:                                   {},
		{"DELETE", "/api/v2/systems/:id"}:                                {},
		{"POST", "/api/v2/systems/reseed"}:                               {},
		{"POST", "/api/v2/systems/:id/metadata"}:                         {},
		{"GET", "/api/v2/systems/:id/metadata"}:                          {},
		{"GET", "/api/v2/systems/by-name/:name/chart"}:                   {},
		{"GET", "/api/v2/systems/by-name/:name/prerequisites"}:           {},
		{"POST", "/api/v2/systems/by-name/:name/prerequisites/:id/mark"}: {},
	}

	for _, route := range engine.Routes() {
		k := key{route.Method, route.Path}
		if _, ok := expected[k]; ok {
			expected[k] = true
		}
		if _, ok := forbidden[k]; ok {
			t.Errorf("portal routes must not expose %s %s", route.Method, route.Path)
		}
	}

	for k, found := range expected {
		if !found {
			t.Errorf("expected route %s %s to be registered", k.method, k.path)
		}
	}
}
