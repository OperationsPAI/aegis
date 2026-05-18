package cluster

import (
	"testing"

	"aegis/platform/framework"

	"github.com/gin-gonic/gin"
)

func TestRoutesPortalReturnsPortalAudience(t *testing.T) {
	reg := RoutesPortal(&Handler{})
	if reg.Audience != framework.AudiencePortal {
		t.Fatalf("expected AudiencePortal, got %q", reg.Audience)
	}
	if reg.Name != "cluster" {
		t.Fatalf("expected name %q, got %q", "cluster", reg.Name)
	}
	if reg.Register == nil {
		t.Fatal("expected Register to be non-nil")
	}
}

func TestRoutesPortalRegistersClusterStatus(t *testing.T) {
	reg := RoutesPortal(&Handler{})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v2 := engine.Group("/api/v2")
	reg.Register(v2)

	var found bool
	for _, route := range engine.Routes() {
		if route.Path == "/api/v2/cluster/status" {
			found = true
			if route.Method != "GET" {
				t.Errorf("expected GET on /cluster/status, got %s", route.Method)
			}
		}
	}
	if !found {
		t.Error("expected /api/v2/cluster/status to be registered")
	}
}
