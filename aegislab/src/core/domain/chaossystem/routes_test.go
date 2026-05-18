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

	// Mutations withheld from Portal — write operations that belong to
	// the Admin audience.
	mutationsWithheld := map[key]struct{}{
		{"POST", "/api/v2/systems"}:                                      {},
		{"PUT", "/api/v2/systems/:id"}:                                   {},
		{"DELETE", "/api/v2/systems/:id"}:                                {},
		{"POST", "/api/v2/systems/reseed"}:                               {},
		{"POST", "/api/v2/systems/:id/metadata"}:                         {},
		{"POST", "/api/v2/systems/by-name/:name/prerequisites/:id/mark"}: {},
	}

	// Admin-only reads withheld from Portal — these are GETs, but the
	// data they expose (raw metadata, helm chart blobs, prerequisite
	// status) is operator-facing and not needed by the InjectionCreate
	// wizard. Being a GET is not the determining factor; audience scope is.
	adminOnlyReadsWithheld := map[key]struct{}{
		{"GET", "/api/v2/systems/:id/metadata"}:                {},
		{"GET", "/api/v2/systems/by-name/:name/chart"}:         {},
		{"GET", "/api/v2/systems/by-name/:name/prerequisites"}: {},
	}

	for _, route := range engine.Routes() {
		k := key{route.Method, route.Path}
		if _, ok := expected[k]; ok {
			expected[k] = true
		}
		if _, ok := mutationsWithheld[k]; ok {
			t.Errorf("portal routes must not expose mutation %s %s", route.Method, route.Path)
		}
		if _, ok := adminOnlyReadsWithheld[k]; ok {
			t.Errorf("portal routes must not expose admin-only read %s %s", route.Method, route.Path)
		}
	}

	for k, found := range expected {
		if !found {
			t.Errorf("expected route %s %s to be registered", k.method, k.path)
		}
	}
}
