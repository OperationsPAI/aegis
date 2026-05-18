package chaossystem

import (
	"testing"

	"aegis/platform/framework"

	"github.com/gin-gonic/gin"
)

func TestRoutesMetadata(t *testing.T) {
	handler := &Handler{}
	reg := Routes(handler)

	if reg.Audience != framework.AudiencePortal {
		t.Fatalf("expected AudiencePortal, got %q", reg.Audience)
	}
	if reg.Name != "chaossystem" {
		t.Fatalf("expected name %q, got %q", "chaossystem", reg.Name)
	}
	if reg.Register == nil {
		t.Fatal("expected Register to be non-nil")
	}
}

func TestRoutesRegistersEverythingExactlyOnce(t *testing.T) {
	handler := &Handler{}
	reg := Routes(handler)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v2 := engine.Group("/api/v2")
	reg.Register(v2)

	type key struct {
		method string
		path   string
	}
	expected := map[key]int{
		// Open reads (portal + admin)
		{"GET", "/api/v2/systems"}:                                 0,
		{"GET", "/api/v2/systems/:id"}:                             0,
		{"GET", "/api/v2/systems/by-name/:name/inject-candidates"}: 0,

		// Operator reads (system_read gated)
		{"GET", "/api/v2/systems/:id/metadata"}:                0,
		{"GET", "/api/v2/systems/by-name/:name/chart"}:         0,
		{"GET", "/api/v2/systems/by-name/:name/prerequisites"}: 0,

		// Writes (system_configure gated)
		{"POST", "/api/v2/systems"}:                                      0,
		{"PUT", "/api/v2/systems/:id"}:                                   0,
		{"POST", "/api/v2/systems/:id/metadata"}:                         0,
		{"POST", "/api/v2/systems/reseed"}:                               0,
		{"POST", "/api/v2/systems/by-name/:name/prerequisites/:id/mark"}: 0,

		// Delete (system_manage gated)
		{"DELETE", "/api/v2/systems/:id"}: 0,
	}

	for _, route := range engine.Routes() {
		k := key{route.Method, route.Path}
		count, ok := expected[k]
		if !ok {
			t.Errorf("unexpected route registered: %s %s", route.Method, route.Path)
			continue
		}
		expected[k] = count + 1
	}

	for k, count := range expected {
		if count == 0 {
			t.Errorf("expected route %s %s to be registered, but it was not", k.method, k.path)
		}
		if count > 1 {
			t.Errorf("route %s %s registered %d times — duplicate registration", k.method, k.path, count)
		}
	}
}
