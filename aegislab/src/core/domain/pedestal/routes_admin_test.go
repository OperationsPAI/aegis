package pedestal

import (
	"testing"

	"aegis/platform/framework"

	"github.com/gin-gonic/gin"
)

// TestRoutesAdminReturnsAdminAudience pins the audience + name so a
// refactor that accidentally swapped the registrar's audience (admin →
// portal) would surface here rather than as a 404 in integration.
func TestRoutesAdminReturnsAdminAudience(t *testing.T) {
	// We pass nil because the registrar function itself does not deref
	// the handler — Register is a closure that only runs when invoked
	// against a router.
	reg := RoutesAdmin(nil)

	if reg.Audience != framework.AudienceAdmin {
		t.Fatalf("expected AudienceAdmin, got %q", reg.Audience)
	}
	if reg.Name != "pedestal.admin" {
		t.Fatalf("expected name %q, got %q", "pedestal.admin", reg.Name)
	}
	if reg.Register == nil {
		t.Fatal("expected Register to be non-nil")
	}
}

// TestRoutesAdminRegistersExpectedPaths walks the registered gin routes to
// confirm every endpoint promised by the task brief is wired in. A
// silently-missing endpoint (e.g. forgotten DELETE) would have been hard
// to spot otherwise — the integration test surface is the only other
// caller of these paths.
func TestRoutesAdminRegistersExpectedPaths(t *testing.T) {
	// The handler can be a non-nil zero value here because the closure
	// only references handler.* method pointers, not its fields.
	handler := &RuntimeHandler{}
	reg := RoutesAdmin(handler)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v2 := engine.Group("/api/v2")
	reg.Register(v2)

	expected := map[string]string{
		"GET /api/v2/pedestals":                     "list",
		"GET /api/v2/pedestals/:release":            "get",
		"POST /api/v2/pedestals":                    "install",
		"POST /api/v2/pedestals/:release/restart":   "restart",
		"DELETE /api/v2/pedestals/:release":         "uninstall",
	}
	found := map[string]bool{}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := expected[key]; ok {
			found[key] = true
		}
	}

	for key := range expected {
		if !found[key] {
			t.Errorf("expected route %s to be registered", key)
		}
	}
}
