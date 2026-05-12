package metric

import (
	"testing"

	"aegis/platform/framework"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestEngine() *gin.Engine {
	return gin.New()
}

func TestRoutesSDKReturnsSDKAudience(t *testing.T) {
	reg := RoutesSDK(&Handler{})
	if reg.Audience != framework.AudienceSDK {
		t.Fatalf("expected audience %q, got %q", framework.AudienceSDK, reg.Audience)
	}
	if reg.Name != "metric.sdk" {
		t.Fatalf("expected name %q, got %q", "metric.sdk", reg.Name)
	}
	if reg.Register == nil {
		t.Fatal("expected non-nil Register function")
	}
}

func TestRoutesSDKRegistersExpectedPaths(t *testing.T) {
	reg := RoutesSDK(&Handler{})

	engine := newTestEngine()
	reg.Register(engine.Group("/api/v2"))

	routes := engine.Routes()
	expected := map[string]bool{
		"/api/v2/metrics/algorithms": false,
		"/api/v2/metrics/executions": false,
		"/api/v2/metrics/injections": false,
	}

	for _, r := range routes {
		if _, ok := expected[r.Path]; ok {
			expected[r.Path] = true
		}
	}

	for path, found := range expected {
		if !found {
			t.Errorf("expected route %q to be registered", path)
		}
	}
}

func TestRoutesSDKAllEndpointsAreGET(t *testing.T) {
	reg := RoutesSDK(&Handler{})

	engine := newTestEngine()
	reg.Register(engine.Group("/api/v2"))

	for _, r := range engine.Routes() {
		if r.Path == "/api/v2/metrics/algorithms" ||
			r.Path == "/api/v2/metrics/executions" ||
			r.Path == "/api/v2/metrics/injections" {
			if r.Method != "GET" {
				t.Errorf("expected GET for %q, got %s", r.Path, r.Method)
			}
		}
	}
}

func TestModuleProvidesRouteRegistrar(t *testing.T) {
	reg := RoutesSDK(&Handler{})
	var _ = reg
}
