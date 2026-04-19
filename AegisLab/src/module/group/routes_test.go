package group

import (
	"aegis/framework"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRoutesPortalReturnsPortalAudience(t *testing.T) {
	handler := &Handler{}
	reg := RoutesPortal(handler)

	if reg.Audience != framework.AudiencePortal {
		t.Fatalf("expected AudiencePortal, got %q", reg.Audience)
	}
	if reg.Name != "group" {
		t.Fatalf("expected name %q, got %q", "group", reg.Name)
	}
	if reg.Register == nil {
		t.Fatal("expected Register to be non-nil")
	}
}

func TestRoutesPortalRegistersExpectedPaths(t *testing.T) {
	handler := &Handler{}
	reg := RoutesPortal(handler)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v2 := engine.Group("/api/v2")
	reg.Register(v2)

	expectedPaths := map[string]bool{
		"/api/v2/groups/:group_id/stats":  false,
		"/api/v2/groups/:group_id/stream": false,
	}

	for _, route := range engine.Routes() {
		if _, ok := expectedPaths[route.Path]; ok {
			if route.Method != "GET" {
				t.Errorf("expected GET for %s, got %s", route.Path, route.Method)
			}
			expectedPaths[route.Path] = true
		}
	}

	for path, found := range expectedPaths {
		if !found {
			t.Errorf("expected route %s to be registered", path)
		}
	}
}
