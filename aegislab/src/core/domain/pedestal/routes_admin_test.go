package pedestal

import (
	"testing"

	"aegis/platform/framework"

	"github.com/gin-gonic/gin"
)

// TestRoutesReturnsPortalAudience pins the audience + name so a refactor
// that accidentally swapped the registrar's audience would surface here
// rather than as a 404 in integration.
func TestRoutesReturnsPortalAudience(t *testing.T) {
	// The registrar function only captures handler pointers into a closure;
	// it does not deref them. Passing nils is safe here because we never
	// invoke Register against a router in this test — we only assert the
	// surface metadata (Audience, Name, non-nil Register).
	reg := Routes(nil, nil)

	if reg.Audience != framework.AudiencePortal {
		t.Fatalf("expected AudiencePortal, got %q", reg.Audience)
	}
	if reg.Name != "pedestal" {
		t.Fatalf("expected name %q, got %q", "pedestal", reg.Name)
	}
	if reg.Register == nil {
		t.Fatal("expected Register to be non-nil")
	}
}

// TestRoutesRegistersExpectedPaths walks the registered gin routes to
// confirm every endpoint is wired exactly once.
func TestRoutesRegistersExpectedPaths(t *testing.T) {
	// The handlers can be non-nil zero values because the closure only
	// references method pointers, not their fields.
	reg := Routes(&Handler{}, &RuntimeHandler{})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v2 := engine.Group("/api/v2")
	reg.Register(v2)

	type key struct {
		method string
		path   string
	}
	expected := []key{
		// runtime
		{"GET", "/api/v2/pedestals"},
		{"GET", "/api/v2/pedestals/:release"},
		{"POST", "/api/v2/pedestals"},
		{"POST", "/api/v2/pedestals/:release/restart"},
		{"DELETE", "/api/v2/pedestals/:release"},
		// helm config
		{"GET", "/api/v2/pedestal/helm/:container_version_id"},
		{"POST", "/api/v2/pedestal/helm/:container_version_id/verify"},
		{"PUT", "/api/v2/pedestal/helm/:container_version_id"},
		{"POST", "/api/v2/pedestal/helm/:container_version_id/reseed"},
	}
	counts := make(map[key]int)
	for _, route := range engine.Routes() {
		counts[key{route.Method, route.Path}]++
	}
	for _, e := range expected {
		switch counts[e] {
		case 0:
			t.Errorf("expected route %s %s to be registered", e.method, e.path)
		case 1:
			// good
		default:
			t.Errorf("route %s %s registered %d times", e.method, e.path, counts[e])
		}
	}
}
