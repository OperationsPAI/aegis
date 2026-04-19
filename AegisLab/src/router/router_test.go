package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/framework"
	user "aegis/module/user"

	"github.com/gin-gonic/gin"
)

func TestRouterSeparatesRouteGroups(t *testing.T) {
	engine := NewForTest(&Handlers{}, nil)
	routes := engine.Routes()

	requiredPrefixes := []string{
		"/api/v2/auth",
		"/api/v2/projects",
		"/api/v2/executions",
		"/api/v2/sdk",
		"/api/v2/system/audit",
		"/api/v2/system/configs",
		"/api/v2/system/monitor",
		"/api/v2/system/health",
		"/docs/",
	}

	for _, prefix := range requiredPrefixes {
		if !hasRoutePrefix(routes, prefix) {
			t.Fatalf("expected route prefix %q to be registered", prefix)
		}
	}
}

func TestRouterRegistersModuleContributedRoutes(t *testing.T) {
	engine := New(Params{
		Handlers: &Handlers{},
		Registrars: []framework.RouteRegistrar{
			user.RoutesAdmin(&user.Handler{}),
		},
	})

	if !hasRoutePrefix(engine.Routes(), "/api/v2/users") {
		t.Fatalf("expected user route prefix to be registered from module contribution")
	}
}

func hasRoutePrefix(routes []gin.RouteInfo, prefix string) bool {
	for _, route := range routes {
		if len(route.Path) >= len(prefix) && route.Path[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func TestSwaggerDocEndpointServesRegisteredSpec(t *testing.T) {
	engine := NewForTest(&Handlers{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/docs/doc.json", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected swagger doc endpoint status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "/api/v2/auth/login") {
		t.Fatalf("expected swagger doc to include auth login path")
	}
}
